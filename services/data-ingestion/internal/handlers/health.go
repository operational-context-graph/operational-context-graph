// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package handlers contains the Gin HTTP handlers for the service.
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// readyTimeout bounds the readiness database check so a slow or dead database does not hang the probe.
const readyTimeout = 2 * time.Second

// Pinger reports whether a dependency (the outbox database) is reachable.
// *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Health serves liveness and readiness checks.
type Health struct {
	db Pinger
}

// NewHealth returns a Health handler.
func NewHealth(db Pinger) *Health {
	return &Health{db: db}
}

// Healthz reports process liveness. It never depends on the database: a transient database outage must not get the pod killed.
func (h *Health) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz reports readiness to serve traffic.
func (h *Health) Readyz(c *gin.Context) {
	if h.db != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()
		if err := h.db.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "reason": "database unavailable"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
