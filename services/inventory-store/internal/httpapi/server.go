// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

const routeKey = "route"

// db is the interface the HTTP handlers require from the store layer.
type db interface {
	UpsertAsset(ctx context.Context, in store.AssetInput) (store.Asset, bool, error)
	GetAsset(ctx context.Context, id uuid.UUID) (store.Asset, error)
	ListAssets(ctx context.Context, artifactType, sourceSystem string, limit int, after *store.Cursor) ([]store.Asset, *store.Cursor, error)
	UpsertAssetBatch(ctx context.Context, inputs []store.AssetInput) ([]store.BatchResult, error)
	UpsertRelationship(ctx context.Context, in store.RelationshipInput) (store.Relationship, bool, error)
	ListRelationships(ctx context.Context, assetID uuid.UUID, limit int) ([]store.Relationship, error)
	FilterRelationships(ctx context.Context, assetID, sourceAssetID, targetAssetID *uuid.UUID, artifactType string, limit int) ([]store.Relationship, error)
	GetRelationshipByID(ctx context.Context, id uuid.UUID) (store.Relationship, error)
	UpsertRelationshipBatch(ctx context.Context, inputs []store.RelationshipInput) ([]store.Relationship, error)
	Ping(ctx context.Context) error
	SchemaReady(ctx context.Context) error
}

// NewServer creates a configured Gin-backed HTTP server.
func NewServer(d db, addr string) *http.Server {
	gin.SetMode(gin.ReleaseMode)
	binding.EnableDecoderDisallowUnknownFields = true
	r := gin.New()
	r.Use(requestIDMiddleware())
	r.Use(loggingMiddleware())
	r.Use(panicRecoveryMiddleware())

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

	r.NoRoute(func(c *gin.Context) {
		c.Set(routeKey, "unmatched")
		renderError(c, apperr.ErrNotFound)
	})

	v1 := r.Group("/v1")
	v1.Use(jsonBodyMiddleware(4 * 1024 * 1024)) // 4 MiB cap for POST bodies
	registerAssetRoutes(v1, d)
	registerRelationshipRoutes(v1, d)

	return &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
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

// jsonBodyMiddleware enforces Content-Type: application/json (→ 415) on non-GET/HEAD requests
// and caps the request body to maxBytes (→ 413).
func jsonBodyMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		if c.ContentType() != "application/json" {
			renderError(c, fmt.Errorf("%w: Content-Type must be application/json", apperr.ErrUnsupportedMediaType))
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// panicRecoveryMiddleware recovers from panics, logs them via slog, and returns a 500 problem response.
func panicRecoveryMiddleware() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		route, _ := c.Get(routeKey)
		slog.Error("panic recovered",
			"panic", recovered,
			"route", route,
			"request_id", c.GetHeader("X-Request-Id"),
		)
		renderError(c, fmt.Errorf("internal server error"))
		c.Abort()
	})
}
