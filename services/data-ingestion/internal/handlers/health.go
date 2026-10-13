// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package handlers contains the Gin HTTP handlers for the service.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health serves liveness and readiness checks.
type Health struct{}

// NewHealth returns a Health handler.
func NewHealth() *Health {
	return &Health{}
}

// Healthz reports process liveness.
func (h *Health) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz reports readiness to serve traffic.
func (h *Health) Readyz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
