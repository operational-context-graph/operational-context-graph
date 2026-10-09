// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/handlers"
)

// registerRoutes wires the HTTP routes onto the engine.
func registerRoutes(engine *gin.Engine) {
	health := handlers.NewHealth()
	engine.GET("/healthz", health.Healthz)
	engine.GET("/readyz", health.Readyz)

	// TODO: register the Data Ingestion API routes here once the design in the
	// software design document is implemented, for example:
	//   api := engine.Group("/api/v1")
}
