package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	usecase "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

/*
Controlled request-snapshot contract

This test models two immutable repository views without PostgreSQL or release-state transitions.
Their current versions differ, while both retain the same historical mapping release.
Requests cover discovery, candidate filters, direct packages, and both nested package pages.

Each request MUST enter exactly one read boundary.
Its complete response MUST match the assigned fixture's sequential baseline, including headers and body bytes.
Before-and-after serialization verifies that request handling does not mutate either fixture.
Channels establish ordering explicitly; sleeps do not establish snapshot consistency.
Once workers start, the parent joins them before fatal assertions or shared-fixture comparisons.
*/

type controlledReadKey struct{}

// controlledRead binds one request to its supplied snapshot and counts read-boundary entries.
type controlledRead struct {
	snapshot *artifactTestSnapshot
	calls    atomic.Int64
}

// controlledSnapshotReader pauses callbacks until every caller reaches its assigned read boundary.
// The parent test controls the barrier without changing either supplied snapshot.
type controlledSnapshotReader struct {
	entered chan<- struct{}
	resume  <-chan struct{}
}

func (r controlledSnapshotReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	read := ctx.Value(controlledReadKey{}).(*controlledRead)
	read.calls.Add(1)
	select {
	case r.entered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-r.resume:
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn(ctx, read.snapshot)
}

// TestConcurrentCurrentAndMappingReadsKeepIndependentSnapshots overlaps current discovery, candidate queries,
// direct packages, and nested package pages. Two immutable fixtures expose different current versions while
// historical mapping content remains release-pinned. Each response must match its fixture's baseline and enter
// exactly one read boundary.
func TestConcurrentCurrentAndMappingReadsKeepIndependentSnapshots(t *testing.T) {
	first, second := multiMappingFixture(), multiMappingFixture()
	second.current = model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 2}
	second.releases["2.0.0"] = model.Release{SchemaVersion: "2.0.0", ManifestDigest: fixtureBytes([]byte("second manifest"), "application/json").Digest}
	snapshots := []*artifactTestSnapshot{first, second}
	before := [][]byte{snapshotContents(t, first), snapshotContents(t, second)}
	paths := []string{
		"/v1/registry",
		"/v1/releases/1.0.0/source-mappings?sourceArtifactType=incoming&limit=1",
		"/v1/releases/1.0.0/source-mappings?targetArtifactType=device",
		candidateBase + "?limit=1",
		mappingBase + "/package",
		mappingBase + "-two/package",
		candidateBase + "/packages?limit=1",
	}
	// Capture complete sequential responses before requests share the same concurrent handler.
	page := mappingJSON(t, serveArtifactRequest(artifactTestServer(first), http.MethodGet, paths[len(paths)-1]))
	cursor, ok := page["nextCursor"].(string)
	require.True(t, ok)
	paths = append(paths, paths[len(paths)-1]+"&cursor="+url.QueryEscape(cursor))
	expected := make([]map[string]*httptest.ResponseRecorder, len(snapshots))
	for index, snapshot := range snapshots {
		expected[index] = make(map[string]*httptest.ResponseRecorder, len(paths))
		baseline := artifactTestServer(snapshot)
		for _, path := range paths {
			response := serveArtifactRequest(baseline, http.MethodGet, path)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, snapshot.current.SchemaVersion, response.Header().Get("Schema-Version"))
			if path == "/v1/registry" {
				require.Equal(t, snapshot.current.SchemaVersion, mappingJSON(t, response)["schemaVersion"])
			} else {
				require.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
			}
			expected[index][path] = response
		}
	}

	callers := len(paths) * len(snapshots)
	entered, resume := make(chan struct{}, callers), make(chan struct{})
	reader := controlledSnapshotReader{entered: entered, resume: resume}
	server := artifactTestServer(first)
	server.releases, server.artifacts = release.New(reader), usecase.New(reader)
	handler := server.Handler()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		index    int
		path     string
		read     *controlledRead
		response *httptest.ResponseRecorder
	}
	results := make(chan result, callers)
	var group sync.WaitGroup
	for index, snapshot := range snapshots {
		for _, path := range paths {
			group.Go(func() {
				read := &controlledRead{snapshot: snapshot}
				request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(context.WithValue(ctx, controlledReadKey{}, read))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				results <- result{index: index, path: path, read: read, response: response}
			})
		}
	}
	// Every caller must reach its assigned boundary before any callback proceeds.
	for range callers {
		select {
		case <-entered:
		case <-ctx.Done():
			cancel()
			group.Wait()
			require.FailNow(t, "requests did not enter their independent snapshots before the deadline")
		}
	}
	close(resume)
	group.Wait()
	close(results)
	// Workers return responses; the parent owns assertions and the final mutation check.
	for result := range results {
		require.EqualValues(t, 1, result.read.calls.Load(), "one read boundary per request: %s", result.path)
		require.Equal(t, http.StatusOK, result.response.Code, result.response.Body.String())
		baseline := expected[result.index][result.path]
		require.Equal(t, baseline.Header(), result.response.Header(), result.path)
		require.Equal(t, baseline.Body.Bytes(), result.response.Body.Bytes(), result.path)
	}
	for index, snapshot := range snapshots {
		require.Equal(t, before[index], snapshotContents(t, snapshot), "reads must not mutate supplied repository snapshots")
	}
}

// snapshotContents serializes all repository fixture data for before-and-after mutation checks.
func snapshotContents(t testing.TB, snapshot *artifactTestSnapshot) []byte {
	t.Helper()
	value, err := json.Marshal([]any{
		snapshot.current,
		snapshot.releases,
		snapshot.artifacts,
		snapshot.selections,
		snapshot.collections,
		snapshot.history,
		snapshot.resources,
		snapshot.dependencies,
		snapshot.mappings,
		snapshot.uris,
		snapshot.bindings,
		snapshot.stores,
	})
	require.NoError(t, err)
	return value
}
