// Tests for HTTP lifecycle behavior after the process entrypoint has converted SIGTERM into cancellation
// of Server.Run or Server.RunListener. The tests use a real TCP listener and the production Gin router.
// They do not send an operating-system signal because signal handling belongs to the command package.
// The tests instead isolate the server behavior that follows signal delivery: readiness changes,
// request-context cancellation, admission release, bounded server shutdown, and checker closure.

package httpapi

import (
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

// blockingLifecycleSnapshot decorates a real in-memory release snapshot with a deterministic blocking point.
// It delegates every method except Artifact and Resource to the Snapshot. The configured revision identifies
// the one operation to block.
//
// The started channel closes when a request reaches that real storage-read operation. The canceled channel
// closes only after the request context is canceled. Tests wait for the started channel before cancelling a
// client or the server. This ordering proves that cancellation occurs during request processing rather than
// before the request starts. The once guard makes the started channel safe when a route performs repeated
// reads. Each instance blocks one request and must use one matching revision.
type blockingLifecycleSnapshot struct {
	release.Snapshot
	artifactRevision string
	resourceRevision string
	started          chan struct{}
	canceled         chan struct{}
	once             sync.Once
}

func (s *blockingLifecycleSnapshot) Artifact(ctx context.Context, revision string) (model.Artifact, error) {
	// Exact artifact-byte retrieval reaches Artifact before it validates and
	// writes bytes. Blocking here creates an in-flight exact-byte request.
	if revision == s.artifactRevision {
		return model.Artifact{}, s.wait(ctx)
	}
	return s.Snapshot.Artifact(ctx, revision)
}

func (s *blockingLifecycleSnapshot) Resource(ctx context.Context, revision, name string) (model.Resource, error) {
	// Source-mapping package assembly reaches Resource after it resolves mapping
	// metadata and before it assembles program bytes into its response model.
	// Blocking here therefore covers both direct packages and package pages.
	if revision == s.resourceRevision {
		return model.Resource{}, s.wait(ctx)
	}
	return s.Snapshot.Resource(ctx, revision, name)
}

func (s *blockingLifecycleSnapshot) wait(ctx context.Context) error {
	// Returning ctx.Err makes the production service take its normal canceled
	// read path. No test-only response or storage behavior is introduced.
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	close(s.canceled)
	return ctx.Err()
}

// lifecycleReader supplies a caller-owned snapshot to both release and artifact services.
// Production readers create PostgreSQL snapshots. This helper keeps the lifecycle tests
// deterministic while preserving the same service and route paths that production requests
// use.
type lifecycleReader struct{ snapshot release.Snapshot }

func (r lifecycleReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	return fn(ctx, r.snapshot)
}

// TestSIGTERMShutdownCancelsInFlightReads verifies that shutdown cancels real in-flight API read paths.
// Test cases cover direct package assembly, package page assembly, and exact-byte retrieval. Each subtest
// starts a real HTTP server, waits until the selected snapshot read is blocked, then cancels the RunListener
// context.
//
// The assertions prove that the blocked request receives context cancellation, the server completes its
// bounded drain, the admission slot is released, the readiness checker is closed, and no controlled read-model
// state changes.
func TestSIGTERMShutdownCancelsInFlightReads(t *testing.T) {
	for _, test := range []struct {
		name             string
		path             string
		artifactRevision string
		resourceRevision string
	}{
		{"direct package assembly", "/v1/releases/1.0.0/source-mappings/device-mapping/package", "", "rev-mapping"},
		{"package page assembly", "/v1/releases/1.0.0/schemas/device/source-mappings/packages", "", "rev-mapping"},
		{"exact byte transfer", "/v1/artifacts/rev-schema", "rev-schema", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := conformanceFixture(t)
			before := cloneConformanceFixture(t, fixture)
			blocking := &blockingLifecycleSnapshot{
				Snapshot: fixture, artifactRevision: test.artifactRevision, resourceRevision: test.resourceRevision,
				started: make(chan struct{}), canceled: make(chan struct{}),
			}
			checker := &fakeChecker{}
			server := NewWithServices(testConfig(), checker, release.New(lifecycleReader{snapshot: blocking}), artifact.New(lifecycleReader{snapshot: blocking}))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ctx, sigterm := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- server.RunListener(ctx, listener) }()

			client := &http.Client{Transport: &http.Transport{DisableCompression: true}, Timeout: time.Second}
			request, err := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+test.path, nil)
			require.NoError(t, err)
			request.Header.Set("Accept-Encoding", "gzip")
			requestDone := make(chan error, 1)
			go func() {
				response, requestErr := client.Do(request)
				if response != nil {
					_ = response.Body.Close()
				}
				requestDone <- requestErr
			}()
			<-blocking.started
			sigterm()
			<-blocking.canceled
			require.NoError(t, <-done)
			<-requestDone
			assert.Empty(t, server.admission, "shutdown must release the admission slot")
			assert.True(t, checker.isClosed())
			assert.Equal(t, before, fixture, "HTTP reads and shutdown must not mutate the registry")
		})
	}
}

// TestSIGTERMShutdownPreservesReadOnlyResponses verifies successful response behavior separately from cancellation.
// It retrieves both a direct package and a package page with gzip explicitly enabled, decompresses each response,
// and checks that it remains release-pinned. It then retrieves exact artifact bytes without content coding and
// compares every byte.
//
// The test stops the real listener only after all responses complete. The fixture copy confirms that successful
// reads, response generation, and normal shutdown do not mutate the pre-populated registry model.
func TestSIGTERMShutdownPreservesReadOnlyResponses(t *testing.T) {
	fixture := conformanceFixture(t)
	before := cloneConformanceFixture(t, fixture)
	checker := &fakeChecker{}
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 8
	server := NewWithServices(cfg, checker, release.New(artifactTestReader{snapshot: fixture}), artifact.New(artifactTestReader{snapshot: fixture}))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, sigterm := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}, Timeout: time.Second}
	baseURL := "http://" + listener.Addr().String()
	for _, path := range []string{
		"/v1/releases/1.0.0/source-mappings/device-mapping/package",
		"/v1/releases/1.0.0/schemas/device/source-mappings/packages",
	} {
		request, requestErr := http.NewRequest(http.MethodGet, baseURL+path, nil)
		require.NoError(t, requestErr)
		request.Header.Set("Accept-Encoding", "gzip")
		response, requestErr := client.Do(request)
		require.NoError(t, requestErr)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Equal(t, "gzip", response.Header.Get("Content-Encoding"))
		reader, gzipErr := gzip.NewReader(response.Body)
		require.NoError(t, gzipErr)
		body, readErr := io.ReadAll(reader)
		require.NoError(t, readErr)
		require.NoError(t, reader.Close())
		require.NoError(t, response.Body.Close())
		assert.Contains(t, string(body), `"schemaVersion":"1.0.0"`)
	}

	response, err := client.Get(baseURL + "/v1/artifacts/rev-schema")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, []byte(`{"type":"object"}`), body)

	sigterm()
	require.NoError(t, <-done)
	assert.Empty(t, server.admission, "shutdown must release every admission slot")
	assert.True(t, checker.isClosed())
	assert.Equal(t, before, fixture, "HTTP reads and shutdown must not mutate the registry")
}

// TestClientDisconnectCancelsInFlightPackageAssembly verifies client-driven cancellation independently from
// server shutdown. It sends a raw HTTP request over TCP, waits until direct package assembly blocks while
// loading a program resource, and closes the client connection. The blocking snapshot observes the canceled
// request context. The admission assertion confirms that the disconnected request cannot retain process-wide
// API capacity.
func TestClientDisconnectCancelsInFlightPackageAssembly(t *testing.T) {
	fixture := conformanceFixture(t)
	blocking := &blockingLifecycleSnapshot{
		Snapshot: fixture, resourceRevision: "rev-mapping", started: make(chan struct{}), canceled: make(chan struct{}),
	}
	server := NewWithServices(testConfig(), &fakeChecker{}, release.New(lifecycleReader{snapshot: blocking}), artifact.New(lifecycleReader{snapshot: blocking}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.RunListener(ctx, listener) }()

	connection, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	_, err = connection.Write([]byte("GET /v1/releases/1.0.0/source-mappings/device-mapping/package HTTP/1.1\r\nHost: registry.test\r\n\r\n"))
	require.NoError(t, err)
	<-blocking.started
	require.NoError(t, connection.Close())
	select {
	case <-blocking.canceled:
	case <-time.After(time.Second):
		t.Fatal("client disconnect did not cancel package assembly")
	}
	assert.Eventually(t, func() bool { return len(server.admission) == 0 }, time.Second, time.Millisecond)

	stop()
	require.NoError(t, <-done)
}
