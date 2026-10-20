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

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// Server owns the Gin engine, HTTP lifecycle, admission, and readiness state.
type Server struct {
	cfg       introspection.Config
	checker   postgres.ReadinessChecker
	releases  *release.Service
	artifacts *artifact.Service
	cursors   *pagination.Codec
	// discoveryLinks is server-owned. Tests may replace it before requests.
	discoveryLinks func(string) links
	engine         *gin.Engine
	api            *gin.RouterGroup
	http           *http.Server
	admission      chan struct{}
	draining       atomic.Bool
	startOnce      sync.Once
	closeOnce      sync.Once
}

// New creates a server. The server closes its readiness checker when Run
// returns or cannot start its listener.
func New(cfg introspection.Config, checker postgres.ReadinessChecker) *Server {
	return NewWithReleaseService(cfg, checker, nil)
}

// NewWithReleaseService creates a server with the release-discovery routes.
func NewWithReleaseService(cfg introspection.Config, checker postgres.ReadinessChecker, releases *release.Service) *Server {
	return NewWithServices(cfg, checker, releases, nil)
}

// NewWithServices creates a server with read-only API services.
func NewWithServices(cfg introspection.Config, checker postgres.ReadinessChecker, releases *release.Service, artifacts *artifact.Service) *Server {
	gin.SetMode(gin.ReleaseMode)
	server := &Server{
		cfg: cfg, checker: checker, releases: releases, artifacts: artifacts, admission: make(chan struct{}, cfg.Admission.MaxInFlight),
	}
	server.discoveryLinks = defaultDiscoveryLinks
	if releases != nil {
		previous := make([]pagination.Key, 0, len(cfg.Cursor.PreviousKeys))
		for _, key := range cfg.Cursor.PreviousKeys {
			previous = append(previous, pagination.Key{ID: key.ID, Secret: []byte(key.Secret), Deadline: key.Deadline})
		}
		codec, err := pagination.New(pagination.Key{ID: cfg.Cursor.ActiveKey.ID, Secret: []byte(cfg.Cursor.ActiveKey.Secret), Deadline: cfg.Cursor.ActiveKey.Deadline}, previous)
		if err != nil {
			panic("validated cursor configuration produced invalid codec")
		}
		server.cursors = codec
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.GET("/healthz", server.healthz)
	engine.HEAD("/healthz", server.healthz)
	engine.GET("/readyz", server.readyz)
	engine.HEAD("/readyz", server.readyz)

	api := engine.Group("/v1")
	api.Use(server.admit, server.validateRequest)
	server.api = api
	server.registerReleaseRoutes()
	server.registerArtifactRoutes()
	// Keep a scoped endpoint so lifecycle tests can verify admission.
	api.GET("/", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	server.engine = engine
	server.http = &http.Server{
		Addr: net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), Handler: engine,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, ReadTimeout: cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout, IdleTimeout: cfg.Server.IdleTimeout,
		// Include the bounded request target because net/http applies this limit
		// to the request line and headers before Gin receives the request.
		MaxHeaderBytes: maxRequestTargetBytes + maxHeaderBytes,
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
		WriteError(c.Writer, ReadLimitExceeded, "", nil, s.cfg.Admission.RetryInterval)
		c.Abort()
	}
}

func (s *Server) validateRequest(c *gin.Context) {
	if err := ValidateRequestMetadata(c.Request); err != nil {
		WriteError(c.Writer, InvalidRequest, "", nil, 0)
		c.Abort()
		return
	}
	c.Next()
}
