// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

type fakeChecker struct {
	mu     sync.Mutex
	err    error
	closed bool
	calls  int
}

func (f *fakeChecker) Ready(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

func (f *fakeChecker) readyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeChecker) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeChecker) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeChecker) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

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

func TestServerBoundsHeaderBlockBeforeGin(t *testing.T) {
	server := New(testConfig(), &fakeChecker{})
	assert.Equal(t, maxRequestTargetBytes+maxHeaderBytes, server.http.MaxHeaderBytes)
	handlerCalls := make(chan struct{}, 1)
	server.http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handlerCalls <- struct{}{}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()

	connection, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = connection.Close() }()
	_, err = fmt.Fprintf(connection, "GET /healthz HTTP/1.1\r\nHost: example.test\r\nX-Excess: %s\r\n\r\n", strings.Repeat("a", server.http.MaxHeaderBytes+8*1024))
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusRequestHeaderFieldsTooLarge, response.StatusCode)
	assert.Empty(t, handlerCalls)

	cancel()
	require.NoError(t, <-done)
}

func TestReadinessFailsWhenDrainingOrUnavailable(t *testing.T) {
	checker := &fakeChecker{err: errors.New("unavailable")}
	server := New(testConfig(), checker)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)

	checker.setError(nil)
	server.Drain()
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestAdmissionRejectsWhenCapacityIsExhausted(t *testing.T) {
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 1
	checker := &fakeChecker{}
	server := New(cfg, checker)
	poolAcquisitions := 0
	server.api.GET("/pool", func(c *gin.Context) {
		poolAcquisitions++
		c.Status(http.StatusNoContent)
	})
	server.admission <- struct{}{}
	defer func() { <-server.admission }()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/pool", nil))
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	assert.Equal(t, "1", response.Header().Get("Retry-After"))
	assert.Contains(t, response.Body.String(), `"code":"read-limit-exceeded"`)
	assert.Empty(t, response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))
	assert.Zero(t, poolAcquisitions)
	assert.Zero(t, checker.readyCalls())
}

func TestCapacityOneCompletesAndReleasesAdmission(t *testing.T) {
	server := New(testConfig(), &fakeChecker{})
	calls := 0
	server.api.GET("/work", func(c *gin.Context) {
		calls++
		c.Status(http.StatusNoContent)
	})
	for range 2 {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/work", nil))
		assert.Equal(t, http.StatusNoContent, response.Code)
	}
	assert.Equal(t, 2, calls)
	assert.Empty(t, server.admission)
}

func TestAPIMiddlewareRejectsInvalidRequestBeforeHandler(t *testing.T) {
	server := New(testConfig(), &fakeChecker{})
	handlerCalls := 0
	server.api.GET("/validated", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/validated", nil)
	request.Header.Add("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"invalid-request"`)
	assert.Zero(t, handlerCalls)
	assert.Empty(t, server.admission)
}

func TestCancellationReleasesAdmissionSlot(t *testing.T) {
	server := New(testConfig(), &fakeChecker{})
	started := make(chan struct{})
	server.api.GET("/wait", func(c *gin.Context) {
		close(started)
		<-c.Request.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/v1/wait", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not release its admission slot")
	}
	assert.Empty(t, server.admission)
}

func TestReadinessRetainsCapacityWhenAPISlotsAreSaturated(t *testing.T) {
	checker := &fakeChecker{}
	server := New(testConfig(), checker)
	server.admission <- struct{}{}
	defer func() { <-server.admission }()

	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != http.StatusOK {
				t.Errorf("readiness status = %d, want %d", response.Code, http.StatusOK)
			}
		}()
	}
	group.Wait()
	assert.Equal(t, 32, checker.readyCalls())
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
		assert.True(t, checker.isClosed())
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
	assert.True(t, server.checker.(*fakeChecker).isClosed())
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
	cfg.Server.ShutdownTimeout = 20 * time.Millisecond
	checker := &fakeChecker{}
	server := New(cfg, checker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	started := make(chan struct{})
	release := make(chan struct{})
	server.http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()
	responseErr := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		responseErr <- err
	}()
	<-started
	cancel()
	require.Error(t, <-done)
	close(release)
	require.Error(t, <-responseErr)
	assert.True(t, checker.isClosed())
}

func TestShutdownCancelsRequestContext(t *testing.T) {
	checker := &fakeChecker{}
	server := New(testConfig(), checker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	started := make(chan struct{})
	canceled := make(chan struct{})
	server.http.Handler = http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(canceled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()
	go func() {
		_, _ = http.Get("http://" + listener.Addr().String())
	}()
	<-started
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request context was not cancelled")
	}
	require.NoError(t, <-done)
	assert.True(t, checker.isClosed())
}

func testConfig() introspection.Config {
	return introspection.Config{
		Server:    introspection.ServerConfig{Host: "127.0.0.1", Port: 0, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second},
		Database:  introspection.DatabaseConfig{QueryTimeout: time.Second},
		Admission: introspection.AdmissionConfig{MaxInFlight: 1, RetryInterval: time.Second},
	}
}
