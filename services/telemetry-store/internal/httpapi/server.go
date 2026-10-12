// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/operational-context-graph/telemetry-store/internal/store"
)

const routeKey = "route"

// telemetryDB is the interface HTTP handlers require from the store layer.
type telemetryDB interface {
	QueryLogs(ctx context.Context, q store.LogQuery) ([]store.Log, error)
	QueryMetrics(ctx context.Context, q store.MetricQuery) ([]store.Metric, error)
	QueryTraceByID(ctx context.Context, traceID string, from, to time.Time) ([]store.Trace, error)
	Ping(ctx context.Context) error
	SchemaReady(ctx context.Context) error
}

// NewServer creates a configured Gin-backed HTTP server.
func NewServer(d telemetryDB, addr string) *http.Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(requestIDMiddleware())
	r.Use(loggingMiddleware())
	r.Use(gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		if err := d.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy"})
			return
		}
		if err := d.SchemaReady(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "schema not ready"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1/telemetry")
	registerTelemetryRoutes(v1, d)

	return &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = uuid.New().String()
		}
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route, _ := c.Get(routeKey)
		slog.Info("request",
			"route", route,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"request_id", c.GetHeader("X-Request-Id"),
		)
	}
}

// setRoute stores the stable route name in the Gin context for the logging middleware.
func setRoute(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(routeKey, name)
		c.Next()
	}
}
