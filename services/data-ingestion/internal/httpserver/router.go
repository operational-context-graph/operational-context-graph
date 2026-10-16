// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"github.com/gin-gonic/gin"
)

// registerRoutes wires the HTTP routes onto the engine.
func registerRoutes(engine *gin.Engine) {
	h := newHealth()
	engine.GET("/healthz", h.healthz)
	engine.GET("/readyz", h.readyz)
}
