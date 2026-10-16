// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package httpapi owns the Schema Introspection API HTTP lifecycle.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// Server owns the Gin engine, HTTP lifecycle, admission, and readiness state.
type Server struct {
	cfg       introspection.Config
	checker   postgres.ReadinessChecker
	engine    *gin.Engine
	http      *http.Server
	admission chan struct{}
	draining  atomic.Bool
	startOnce sync.Once
	closeOnce sync.Once
}

// New creates a server. The server closes its readiness checker when Run
// returns or cannot start its listener.
func New(cfg introspection.Config, checker postgres.ReadinessChecker) *Server {
	gin.SetMode(gin.ReleaseMode)
	server := &Server{
		cfg: cfg, checker: checker, admission: make(chan struct{}, cfg.Admission.MaxInFlight),
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.GET("/healthz", server.healthz)
	engine.HEAD("/healthz", server.healthz)
	engine.GET("/readyz", server.readyz)
	engine.HEAD("/readyz", server.readyz)

	api := engine.Group("/v1")
	api.Use(server.admit)
	// Phase four registers API resources on this group. Keep a scoped endpoint so
	// shutdown tests can verify admission before the route table is complete.
	api.GET("/", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	server.engine = engine
	server.http = &http.Server{
		Addr: net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), Handler: engine,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, ReadTimeout: cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout, IdleTimeout: cfg.Server.IdleTimeout,
	}

	return server
}

// Handler exposes the server for in-process tests.
func (s *Server) Handler() http.Handler { return s.engine }

// Drain stops readiness before refusing new API admission.
func (s *Server) Drain() {
	s.draining.Store(true)
}

// Run starts serving and handles cancellation with bounded shutdown.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		s.close()
		return fmt.Errorf("listen: %w", err)
	}
	return s.RunListener(ctx, listener)
}

// RunListener runs using a caller-provided listener for lifecycle tests.
func (s *Server) RunListener(ctx context.Context, listener net.Listener) error {
	defer s.close()
	var cancelRequests context.CancelFunc
	s.startOnce.Do(func() {
		baseCtx, cancel := context.WithCancel(context.Background())
		cancelRequests = cancel
		s.http.BaseContext = func(net.Listener) context.Context { return baseCtx }
	})
	if cancelRequests == nil {
		return errors.New("server cannot be started more than once")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.Serve(listener) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		s.Drain()
		// Flip readiness before admission closes so an orchestrator stops routing.
		s.http.SetKeepAlivesEnabled(false)
		cancelRequests()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
		err := s.http.Shutdown(shutdownCtx)
		cancel()
		if err != nil {
			_ = s.http.Close()
		}
		s.close()
		if err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}

func (s *Server) close() {
	s.closeOnce.Do(s.checker.Close)
}

func (s *Server) healthz(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (s *Server) readyz(c *gin.Context) {
	if s.draining.Load() {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.Database.QueryTimeout)
	defer cancel()
	if err := s.checker.Ready(ctx); err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusOK)
}

func (s *Server) admit(c *gin.Context) {
	if s.draining.Load() {
		c.Status(http.StatusServiceUnavailable)
		c.Abort()
		return
	}
	select {
	case s.admission <- struct{}{}:
		defer func() { <-s.admission }()
		c.Next()
	default:
		if s.cfg.Admission.RetryInterval > 0 {
			c.Header("Retry-After", strconv.Itoa(int(s.cfg.Admission.RetryInterval.Round(time.Second)/time.Second)))
		}
		c.Header("Cache-Control", "no-cache")
		c.Status(http.StatusTooManyRequests)
		c.Abort()
	}
}
