// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/handlers"
)

// registerRoutes wires the HTTP routes onto the engine. db is passed to the
// readiness probe; it may be nil when no database is configured.
func registerRoutes(engine *gin.Engine, db handlers.Pinger) {
	health := handlers.NewHealth(db)
	engine.GET("/healthz", health.Healthz)
	engine.GET("/readyz", health.Readyz)
}
