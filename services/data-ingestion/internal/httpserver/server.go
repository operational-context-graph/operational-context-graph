// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package httpserver builds and runs the Gin HTTP server for the service.
package httpserver

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
)

// Server owns the Gin engine and the underlying HTTP server lifecycle.
type Server struct {
	cfg    *config.Config
	engine *gin.Engine
	http   *http.Server
}

// New constructs a Server from the given configuration. It uses gin.New with
// explicit middleware (not gin.Default) so tests and logging stay predictable.
func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()
	engine.Use(gin.Recovery())

	registerRoutes(engine)

	return &Server{
		cfg:    cfg,
		engine: engine,
		http: &http.Server{
			Addr:              net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
			Handler:           engine,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
}

// Handler exposes the underlying HTTP handler for in-process testing.
func (s *Server) Handler() http.Handler {
	return s.engine
}

// Run starts the server and blocks until ctx is cancelled or the server fails.
// On cancellation it attempts a graceful shutdown with a bounded timeout.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	}
}
