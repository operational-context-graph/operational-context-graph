package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// health serves liveness and readiness checks.
type health struct {
	ping func(context.Context) error
}

// newHealth returns a health handler.
func newHealth(ping func(context.Context) error) *health {
	return &health{ping: ping}
}

// healthz reports process liveness.
func (h *health) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// readyz reports readiness to serve traffic. It pings the database with a 1 s
// deadline to catch connection-pool exhaustion or a missing migration.
func (h *health) readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()

	if err := h.ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "database"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
