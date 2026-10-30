// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

type blockingConformanceReader struct {
	started  chan struct{}
	finished chan struct{}
}

func (r blockingConformanceReader) Read(ctx context.Context, _ func(context.Context, release.Snapshot) error) error {
	close(r.started)
	<-ctx.Done()
	close(r.finished)
	return ctx.Err()
}

// TestRouteConformanceFixture provides a stable precondition for the route
// matrix. The fixture represents one pre-populated read-only release snapshot.
func TestRouteConformanceFixture(t *testing.T) {
	fixture := conformanceFixture(t)
	assert.Equal(t, "1.0.0", fixture.current.SchemaVersion)
	assert.Contains(t, fixture.releases, "1.0.0")
	assert.Contains(t, fixture.selections, "schema/device")
	assert.Contains(t, fixture.selections, "source-mapping/device-mapping")
}

// TestHTTPRouteConformance sends every request through a real HTTP client, the
// Gin router, API middleware, handlers, services, and the controlled repository
// snapshot. Focused route tests assert route-specific response fields in detail.
func TestHTTPRouteConformance(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		wantStatus  int
		mediaType   string
		bodySnippet string
	}{
		{"registry", "/v1/registry", http.StatusOK, "application/json", `"schemaVersion":"1.0.0"`},
		{"release collection", "/v1/releases", http.StatusOK, "application/json", `"items":[]`},
		{"release descriptor", "/v1/releases/1.0.0", http.StatusOK, "application/json", `"schemaVersion":"1.0.0"`},
		{"manifest", "/v1/releases/1.0.0/manifest", http.StatusOK, "application/vnd.apeiro.ocg.schema-release-manifest.v1+json", `"schemaVersion":"1.0.0"`},
		{"changes validation", "/v1/changes?from=1.0.0&to=1.0.0", http.StatusBadRequest, "application/json", `"code":"versions-not-comparable"`},
		{"release artifacts", "/v1/releases/1.0.0/artifacts", http.StatusOK, "application/json", `"artifactType":"device"`},
		{"release artifact", "/v1/releases/1.0.0/artifacts/schema/device", http.StatusOK, "application/json", `"artifactRevision":"rev-schema"`},
		{"immutable metadata", "/v1/artifacts/rev-schema/metadata", http.StatusOK, "application/json", `"artifactRevision":"rev-schema"`},
		{"resource", "/v1/artifacts/rev-schema/resources/example", http.StatusOK, "text/plain", "resource"},
		{"payload", "/v1/artifacts/rev-schema", http.StatusOK, "application/schema+json", `{"type":"object"}`},
		{"canonical resource", "/v1/resources/schema/device/rev-schema", http.StatusOK, "application/schema+json", `{"type":"object"}`},
		{"uri resolution", "/v1/releases/1.0.0/resolve?uri=https%3A%2F%2Fexample.test%2Fdevice", http.StatusTemporaryRedirect, "", ""},
		{"artifact history", "/v1/artifact-kinds/schema/device/history", http.StatusOK, "application/json", `"items"`},
		{"mapping package", "/v1/releases/1.0.0/source-mappings/device-mapping/package", http.StatusOK, "application/json", `"programs"`},
		{"mapping", "/v1/releases/1.0.0/source-mappings/device-mapping", http.StatusOK, "application/json", `"engine"`},
		{"source mapping metadata", "/v1/releases/1.0.0/schemas/device/source-mappings", http.StatusOK, "application/json", `"items"`},
		{"source mapping packages", "/v1/releases/1.0.0/schemas/device/source-mappings/packages", http.StatusOK, "application/json", `"items"`},
		{"contexts", "/v1/releases/1.0.0/schemas/device/contexts", http.StatusOK, "application/json", `"items"`},
		{"schema package", "/v1/releases/1.0.0/schemas/device/package", http.StatusOK, "application/json", `"contexts"`},
		{"schema content", "/v1/releases/1.0.0/schemas/device/content", http.StatusOK, "application/schema+json", `{"type":"object"}`},
		{"schema stores", "/v1/releases/1.0.0/schemas/device/stores", http.StatusOK, "application/json", `"stores"`},
		{"schema metadata", "/v1/releases/1.0.0/schemas/device", http.StatusOK, "application/json", `"schemaClass":"entity-type"`},
		{"schema collection", "/v1/releases/1.0.0/schemas", http.StatusOK, "application/json", `"items"`},
		{"mapping collection", "/v1/releases/1.0.0/source-mappings", http.StatusOK, "application/json", `"items"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, baseURL := newHTTPConformanceClient(t, conformanceServer(t))
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method, func(t *testing.T) {
					response := requestConformance(t, client, method, baseURL+test.path, nil)
					closeConformanceBody(t, response)
					assert.Equal(t, test.wantStatus, response.StatusCode)
					assert.Equal(t, "no-cache", response.Header.Get("Cache-Control"))
					if test.mediaType != "" {
						assert.Equal(t, test.mediaType, response.Header.Get("Content-Type"))
					}
					if test.wantStatus == http.StatusOK {
						assert.Equal(t, "1.0.0", response.Header.Get("Schema-Version"))
						assert.NotEmpty(t, response.Header.Get("ETag"))
					}
					if method == http.MethodHead {
						assert.Empty(t, readConformanceBody(t, response))
					} else if test.bodySnippet != "" {
						assert.Contains(t, readConformanceBody(t, response), test.bodySnippet)
					}
				})
			}
		})
	}
}

// TestHTTPDrainConformance verifies the externally visible ordering of drain:
// readiness fails before newly admitted versioned requests can proceed.
func TestHTTPDrainConformance(t *testing.T) {
	server := conformanceServer(t)
	client, baseURL := newHTTPConformanceClient(t, server)
	server.Drain()

	ready := requestConformance(t, client, http.MethodGet, baseURL+"/readyz", nil)
	closeConformanceBody(t, ready)
	assert.Equal(t, http.StatusServiceUnavailable, ready.StatusCode)

	api := requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", nil)
	closeConformanceBody(t, api)
	assert.Equal(t, http.StatusServiceUnavailable, api.StatusCode)
	assert.Equal(t, "no-cache", api.Header.Get("Cache-Control"))
}

func TestHTTPFailureConformance(t *testing.T) {
	t.Run("conditional, negotiation, missing, and invalid-cursor statuses traverse the API", func(t *testing.T) {
		server := conformanceServer(t)
		client, baseURL := newHTTPConformanceClient(t, server)

		first := requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", nil)
		tag := first.Header.Get("ETag")
		require.NoError(t, first.Body.Close())
		conditional := requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", http.Header{"If-None-Match": {tag}})
		closeConformanceBody(t, conditional)
		assert.Equal(t, http.StatusNotModified, conditional.StatusCode)
		assert.Equal(t, tag, conditional.Header.Get("ETag"))
		assert.Empty(t, readConformanceBody(t, conditional))

		missing := requestConformance(t, client, http.MethodGet, baseURL+"/v1/releases/1.0.0/schemas/missing", nil)
		closeConformanceBody(t, missing)
		assert.Equal(t, http.StatusNotFound, missing.StatusCode)
		assert.Contains(t, readConformanceBody(t, missing), `"code":"schema-not-in-release"`)

		unacceptable := requestConformance(t, client, http.MethodGet, baseURL+"/v1/releases/1.0.0/schemas/device/package", http.Header{"Accept": {"text/plain"}})
		closeConformanceBody(t, unacceptable)
		assert.Equal(t, http.StatusNotAcceptable, unacceptable.StatusCode)
		assert.Contains(t, readConformanceBody(t, unacceptable), `"code":"not-acceptable"`)

		invalid := requestConformance(t, client, http.MethodGet, baseURL+"/v1/artifact-kinds/schema/device/history?cursor=invalid", nil)
		closeConformanceBody(t, invalid)
		assert.Equal(t, http.StatusBadRequest, invalid.StatusCode)
		assert.Contains(t, readConformanceBody(t, invalid), `"code":"invalid-cursor"`)
	})

	t.Run("client cancellation releases API admission", func(t *testing.T) {
		server := conformanceServer(t)
		reader := blockingConformanceReader{started: make(chan struct{}), finished: make(chan struct{})}
		server.releases = release.New(reader)
		client, baseURL := newHTTPConformanceClient(t, server)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/registry", nil)
		require.NoError(t, err)
		request.Header.Set("Accept-Encoding", "identity")
		result := make(chan error, 1)
		go func() {
			response, requestErr := client.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			result <- requestErr
		}()
		<-reader.started
		cancel()
		select {
		case <-reader.finished:
		case <-time.After(time.Second):
			t.Fatal("cancelled request did not reach the repository context")
		}
		assert.Error(t, <-result)
		assert.Eventually(t, func() bool { return len(server.admission) == 0 }, time.Second, time.Millisecond)
	})

	t.Run("outage maps to bounded 503 through the API", func(t *testing.T) {
		server := conformanceServer(t)
		server.releases = release.New(releaseTestReader{err: errors.New("database unavailable")})
		client, baseURL := newHTTPConformanceClient(t, server)
		response := requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", nil)
		closeConformanceBody(t, response)
		assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
		assert.Equal(t, "1", response.Header.Get("Retry-After"))
		assert.Contains(t, readConformanceBody(t, response), `"code":"registry-unavailable"`)
	})

	t.Run("admission overload is versionless and releases capacity", func(t *testing.T) {
		server := conformanceServer(t)
		for range cap(server.admission) {
			server.admission <- struct{}{}
		}
		client, baseURL := newHTTPConformanceClient(t, server)
		response := requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", nil)
		assert.Equal(t, http.StatusTooManyRequests, response.StatusCode)
		assert.Empty(t, response.Header.Get("Schema-Version"))
		assert.Contains(t, readConformanceBody(t, response), `"code":"read-limit-exceeded"`)
		for range cap(server.admission) {
			<-server.admission
		}
		response = requestConformance(t, client, http.MethodGet, baseURL+"/v1/registry", nil)
		closeConformanceBody(t, response)
		assert.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("projection and integrity failures do not return partial success", func(t *testing.T) {
		fixture := conformanceFixture(t)
		corrupt := fixture.selections["schema/device"]
		corrupt.Payload.Digest = "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		fixture.selections["schema/device"] = corrupt
		fixture.artifacts[corrupt.Revision] = corrupt
		client, baseURL := newHTTPConformanceClient(t, artifactTestServer(fixture))
		response := requestConformance(t, client, http.MethodGet, baseURL+"/v1/releases/1.0.0/schemas/device/content", nil)
		closeConformanceBody(t, response)
		assert.Equal(t, http.StatusInternalServerError, response.StatusCode)
		body := readConformanceBody(t, response)
		assert.Contains(t, body, `"code":"inconsistent-registry-state"`)
		assert.NotContains(t, body, `"type":"object"`)

		drift := conformanceFixture(t)
		drift.stores = map[string][]model.StorageEntry{}
		client, baseURL = newHTTPConformanceClient(t, artifactTestServer(drift))
		response = requestConformance(t, client, http.MethodGet, baseURL+"/v1/releases/1.0.0/schemas/device/stores", nil)
		closeConformanceBody(t, response)
		assert.Equal(t, http.StatusInternalServerError, response.StatusCode)
		assert.Contains(t, readConformanceBody(t, response), `"code":"inconsistent-registry-state"`)
	})

	t.Run("requests do not mutate controlled read state", func(t *testing.T) {
		fixture := conformanceFixture(t)
		before := cloneConformanceFixture(t, fixture)
		client, baseURL := newHTTPConformanceClient(t, artifactTestServer(fixture))
		response := requestConformance(t, client, http.MethodGet, baseURL+"/v1/releases/1.0.0/schemas/device/package", nil)
		closeConformanceBody(t, response)
		require.Equal(t, http.StatusOK, response.StatusCode)
		assert.True(t, reflect.DeepEqual(before, fixture))
	})
}

func TestHTTPOperationalProbeConformance(t *testing.T) {
	client, baseURL := newHTTPConformanceClient(t, conformanceServer(t))
	for _, path := range []string{"/healthz", "/readyz"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := requestConformance(t, client, method, baseURL+path, nil)
			assert.Equal(t, http.StatusOK, response.StatusCode)
			_ = response.Body.Close()
		}
	}
}

func conformanceServer(t *testing.T) *Server { return artifactTestServer(conformanceFixture(t)) }

func conformanceFixture(t *testing.T) *artifactTestSnapshot {
	t.Helper()
	fixture := mappingFixture()
	manifest := mustTestManifest(t)
	fixture.releases["1.0.0"] = model.Release{
		SchemaVersion: "1.0.0", ActivationSequence: 1,
		ManifestBytes: manifest, ManifestDigest: digest(manifest),
		SourceRepository: "https://git.example.test/schema-model", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	fixture.uris["1.0.0\x00https://example.test/device"] = model.URIEntry{SchemaVersion: "1.0.0", URI: "https://example.test/device", ArtifactRevision: "rev-schema"}
	fixture.history = []model.ReleaseArtifact{{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 1}}
	return fixture
}

func newHTTPConformanceClient(t *testing.T, server *Server) (*http.Client, string) {
	t.Helper()
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return &http.Client{
		Transport:     &http.Transport{DisableCompression: true},
		Timeout:       time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, httpServer.URL
}

func requestConformance(t *testing.T, client *http.Client, method, url string, headers http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	require.NoError(t, err)
	request.Header.Set("Accept-Encoding", "identity")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	return response
}

func readConformanceBody(t *testing.T, response *http.Response) string {
	t.Helper()
	bytes, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return string(bytes)
}

func cloneConformanceFixture(t *testing.T, fixture *artifactTestSnapshot) *artifactTestSnapshot {
	t.Helper()
	// The fixture contains only strings, slices, maps, and model values.
	// A value copy plus copies of the reference containers is sufficient here.
	fixtureCopy := *fixture
	fixtureCopy.releases = mapsClone(fixture.releases)
	fixtureCopy.artifacts = mapsClone(fixture.artifacts)
	fixtureCopy.selections = mapsClone(fixture.selections)
	fixtureCopy.collections = mapsClone(fixture.collections)
	fixtureCopy.resources = mapsClone(fixture.resources)
	fixtureCopy.dependencies = mapsClone(fixture.dependencies)
	fixtureCopy.mappings = mapsClone(fixture.mappings)
	fixtureCopy.uris = mapsClone(fixture.uris)
	fixtureCopy.bindings = mapsClone(fixture.bindings)
	fixtureCopy.stores = mapsClone(fixture.stores)
	fixtureCopy.history = append([]model.ReleaseArtifact(nil), fixture.history...)
	return &fixtureCopy
}

func mapsClone[K comparable, V any](value map[K]V) map[K]V {
	result := make(map[K]V, len(value))
	maps.Copy(result, value)
	return result
}

func closeConformanceBody(t testing.TB, response *http.Response) {
	t.Helper()
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
}
