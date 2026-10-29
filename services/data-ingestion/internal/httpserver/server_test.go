// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
)

func testConfig() *config.ServerConfig {
	return &config.ServerConfig{Host: "127.0.0.1", Port: 8080}
}

func TestServerHealthRoute(t *testing.T) {
	srv := New(testConfig(), nopPing)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

func TestServerReadyRoute(t *testing.T) {
	srv := New(testConfig(), nopPing)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(testConfig(), nopPing)
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	cancel()
	require.NoError(t, <-errc)
}

func TestRunListenerError(t *testing.T) {
	// Bind the port first so srv.Run fails immediately.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	_, portStr, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	cfg := &config.ServerConfig{Host: "127.0.0.1", Port: port}
	srv := New(cfg, nopPing)
	err = srv.Run(context.Background())
	require.Error(t, err)
}
