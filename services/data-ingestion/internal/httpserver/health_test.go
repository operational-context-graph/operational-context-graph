package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nopPing(_ context.Context) error  { return nil }
func failPing(_ context.Context) error { return errors.New("dial tcp: connection refused") }

func TestHealthzHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newHealth(nopPing)
	r.GET("/healthz", h.healthz)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

func TestReadyzHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newHealth(nopPing)
	r.GET("/readyz", h.readyz)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

func TestReadyzHandler_DBDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newHealth(failPing)
	r.GET("/readyz", h.readyz)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.JSONEq(t, `{"status":"unavailable","reason":"database"}`, w.Body.String())
}
