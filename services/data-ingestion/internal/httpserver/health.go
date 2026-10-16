// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// health serves liveness and readiness checks.
type health struct{}

// newHealth returns a health handler.
func newHealth() *health {
	return &health{}
}

// healthz reports process liveness.
func (h *health) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// readyz reports readiness to serve traffic.
func (h *health) readyz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
