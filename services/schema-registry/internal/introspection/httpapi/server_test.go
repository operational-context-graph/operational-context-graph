// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

type fakeChecker struct {
	err    error
	closed bool
}

func (f *fakeChecker) Ready(context.Context) error { return f.err }
func (f *fakeChecker) Close()                      { f.closed = true }

func TestProbesSupportGetAndHead(t *testing.T) {
	checker := &fakeChecker{}
	server := New(testConfig(), checker)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/healthz", "/readyz"} {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(method, path, nil)
			server.Handler().ServeHTTP(response, request)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Empty(t, response.Body.String())
		}
	}
}

func TestReadinessFailsWhenDrainingOrUnavailable(t *testing.T) {
	checker := &fakeChecker{err: errors.New("unavailable")}
	server := New(testConfig(), checker)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)

	checker.err = nil
	server.Drain()
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestAdmissionRejectsWhenCapacityIsExhausted(t *testing.T) {
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 1
	server := New(cfg, &fakeChecker{})
	server.admission <- struct{}{}
	defer func() { <-server.admission }()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/", nil))
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
}

func TestRunClosesPoolOnCancellation(t *testing.T) {
	checker := &fakeChecker{}
	server := New(testConfig(), checker)
	listener, err := newListener()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, checker.closed)
	case <-time.After(time.Second):
		t.Fatal("server did not stop within the shutdown deadline")
	}
}

func TestRunListenerRejectsListenerFailure(t *testing.T) {
	listener, err := newListener()
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	server := New(testConfig(), &fakeChecker{})
	err = server.RunListener(context.Background(), listener)
	require.Error(t, err)
}

func TestDrainRefusesNewAdmission(t *testing.T) {
	server := New(testConfig(), &fakeChecker{})
	server.Drain()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestShutdownForceClosesTransportAfterDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.Server.ShutdownTimeout = time.Nanosecond
	checker := &fakeChecker{}
	server := New(cfg, checker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()
	cancel()
	require.NoError(t, <-done)
	assert.True(t, checker.closed)
}

func testConfig() introspection.Config {
	return introspection.Config{
		Server:    introspection.ServerConfig{Host: "127.0.0.1", Port: 0, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second},
		Database:  introspection.DatabaseConfig{QueryTimeout: time.Second},
		Admission: introspection.AdmissionConfig{MaxInFlight: 1, RetryInterval: time.Second},
	}
}
