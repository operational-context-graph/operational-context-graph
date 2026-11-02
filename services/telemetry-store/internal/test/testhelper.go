// Package test provides shared helpers for telemetry-store tests.
package test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// GetRequest records an HTTP GET against the given handler.
func GetRequest(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}
