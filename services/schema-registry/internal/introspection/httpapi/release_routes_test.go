package httpapi

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
)

// releaseTestReader exposes controlled release state or a boundary failure for route tests.
type releaseTestReader struct {
	snapshot *releaseTestSnapshot
	err      error
}

// Read invokes the route's snapshot callback unless an injected storage failure prevents it.
func (r releaseTestReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(ctx, r.snapshot)
}

// releaseTestSnapshot controls release visibility, pagination, lineage, and pointer changes for HTTP contract checks.
type releaseTestSnapshot struct {
	current             model.CurrentState
	releases            []model.Release
	artifacts           map[string]model.Artifact
	hidden              map[string]bool
	page                func(*int64, int64, int) []model.Release
	afterCurrentRelease func()
	ancestor            *bool
	lineageErr          error
}

// CurrentState returns controlled current and paging coordinates.
func (s *releaseTestSnapshot) CurrentState() model.CurrentState { return s.current }

// CurrentRelease resolves fixture state before an optional hook simulates a concurrent pointer change.
func (s *releaseTestSnapshot) CurrentRelease(context.Context) (model.Release, error) {
	result, err := s.Release(context.Background(), s.current.SchemaVersion)
	if s.afterCurrentRelease != nil {
		after := s.afterCurrentRelease
		s.afterCurrentRelease = nil
		after()
	}
	return result, err
}

// Release hides explicitly inactive fixtures and reports absent versions as ErrNotFound.
func (s *releaseTestSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	if s.hidden[version] {
		return model.Release{}, postgres.ErrNotFound
	}
	for _, r := range s.releases {
		if r.SchemaVersion == version {
			return r, nil
		}
	}
	return model.Release{}, postgres.ErrNotFound
}

// ReleasesAt applies exclusive continuation and inclusive ceiling bounds, unless a custom page supplies controlled shapes.
func (s *releaseTestSnapshot) ReleasesAt(_ context.Context, before *int64, max int64, limit int) ([]model.Release, error) {
	if s.page != nil {
		return s.page(before, max, limit), nil
	}
	var result []model.Release
	for _, r := range s.releases {
		if r.ActivationSequence <= max && (before == nil || r.ActivationSequence < *before) {
			result = append(result, r)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

// IsAncestor supplies connected ancestry by default and supports disconnected or corrupt lineage overrides.
func (s *releaseTestSnapshot) IsAncestor(context.Context, string, string) (bool, error) {
	if s.lineageErr != nil {
		return false, s.lineageErr
	}
	if s.ancestor == nil {
		return true, nil
	}
	return *s.ancestor, nil
}

// Artifact supplies comparison bytes, falling back to any configured artifact when no exact fixture revision exists.
func (s *releaseTestSnapshot) Artifact(_ context.Context, revision string) (model.Artifact, error) {
	value, ok := s.artifacts[revision]
	if !ok {
		for _, value = range s.artifacts {
			return value, nil
		}
		return model.Artifact{}, postgres.ErrNotFound
	}
	return value, nil
}

// ReleaseArtifact reports absent selections outside the release-route fixture's scope.
func (*releaseTestSnapshot) ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error) {
	return model.Artifact{}, postgres.ErrNotFound
}

// ReleaseArtifacts reports absent collections outside the release-route fixture's scope.
func (*releaseTestSnapshot) ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error) {
	return nil, postgres.ErrNotFound
}

// ArtifactHistory reports absent logical history in release-route fixtures.
func (*releaseTestSnapshot) ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error) {
	return nil, postgres.ErrNotFound
}

// Resources reports absent resource metadata in release-route fixtures.
func (*releaseTestSnapshot) Resources(context.Context, string) ([]model.Resource, error) {
	return nil, postgres.ErrNotFound
}

// Resource reports absent resource content in release-route fixtures.
func (*releaseTestSnapshot) Resource(context.Context, string, string) (model.Resource, error) {
	return model.Resource{}, postgres.ErrNotFound
}

// Dependencies reports absent projected dependencies outside comparison payload evidence.
func (*releaseTestSnapshot) Dependencies(context.Context, string) ([]model.Dependency, error) {
	return nil, postgres.ErrNotFound
}

// ContextBindings reports absent context projections in release-route fixtures.
func (*releaseTestSnapshot) ContextBindings(context.Context, string, string) ([]model.SchemaContextBinding, error) {
	return nil, postgres.ErrNotFound
}

// StorageEntries reports absent logical store projections in release-route fixtures.
func (*releaseTestSnapshot) StorageEntries(context.Context, string, string) ([]model.StorageEntry, error) {
	return nil, postgres.ErrNotFound
}

// URIEntry reports absent URI catalog projections in release-route fixtures.
func (*releaseTestSnapshot) URIEntry(context.Context, string, string) (model.URIEntry, error) {
	return model.URIEntry{}, postgres.ErrNotFound
}

// SourceMapping reports absent logical mapping details in release-route fixtures.
func (*releaseTestSnapshot) SourceMapping(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// SourceMappingByRevision reports absent revision mapping details in release-route fixtures.
func (*releaseTestSnapshot) SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// TestReleaseDiscoveryAndManifestConditionals verifies discovery links, exact manifest bytes, version headers, HEAD parity, and representation-specific conditional responses.
func TestReleaseDiscoveryAndManifestConditionals(t *testing.T) {
	manifest := mustTestManifest(t)
	release := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, SourceRepository: "https://git.example.test/schema-model", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{release}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	var body v1.Registry
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "1.0.0", body.SchemaVersion)
	assert.Equal(t, release.ManifestDigest, body.ManifestDigest)
	assert.Equal(t, v1.Links{
		"self":           "/v1/registry",
		"release":        "/v1/releases/1.0.0",
		"manifest":       "/v1/releases/1.0.0/manifest",
		"artifacts":      "/v1/releases/1.0.0/artifacts",
		"schemas":        "/v1/releases/1.0.0/schemas",
		"sourceMappings": "/v1/releases/1.0.0/source-mappings",
		"releases":       "/v1/releases",
	}, body.Links)
	tag := response.Header().Get("ETag")
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/registry", nil)
	request.Header.Set("If-None-Match", tag)
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotModified, response.Code)
	assert.Empty(t, response.Body.String())
	server.discoveryLinks = func(version string) v1.Links {
		value := defaultDiscoveryLinks(version)
		value["releases"] = "/v1/release-history"
		return value
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.NotEqual(t, tag, response.Header().Get("ETag"))
	tag = response.Header().Get("ETag")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, tag, response.Header().Get("ETag"))
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Empty(t, response.Body.String())

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	assert.Contains(t, response.Body.String(), `"previousRelease":null`)
	assert.Contains(t, response.Body.String(), `"sourceMappings":"/v1/releases/1.0.0/source-mappings"`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/releases/1.0.0/manifest", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, `"`+release.ManifestDigest+`"`, response.Header().Get("ETag"))
	assert.Equal(t, release.ManifestDigest, response.Header().Get("Manifest-Digest"))
	assert.Empty(t, response.Body.String())
}

// TestRegistryDiscoveryUsesOneControlledCurrentReleaseSnapshot verifies pointer changes cannot mix release identity, digest, links, and response headers.
func TestRegistryDiscoveryUsesOneControlledCurrentReleaseSnapshot(t *testing.T) {
	first := model.Release{SchemaVersion: "1.0.0", ManifestDigest: "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	second := model.Release{SchemaVersion: "1.1.0", ManifestDigest: "sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	snapshot := &releaseTestSnapshot{
		current:  model.CurrentState{SchemaVersion: first.SchemaVersion, ActivationSequence: 1, SnapshotSequence: 1},
		releases: []model.Release{first, second},
	}
	snapshot.afterCurrentRelease = func() {
		snapshot.current = model.CurrentState{SchemaVersion: second.SchemaVersion, ActivationSequence: 2, SnapshotSequence: 2}
	}
	server := releaseTestServer(snapshot)

	firstResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, firstResponse.Code)
	var firstBody v1.Registry
	require.NoError(t, json.Unmarshal(firstResponse.Body.Bytes(), &firstBody))
	assert.Equal(t, first.SchemaVersion, firstResponse.Header().Get("Schema-Version"))
	assert.Equal(t, first.SchemaVersion, firstBody.SchemaVersion)
	assert.Equal(t, first.ManifestDigest, firstBody.ManifestDigest)
	assert.Equal(t, "/v1/releases/1.0.0", firstBody.Links["release"])
	assert.Equal(t, "/v1/releases/1.0.0/manifest", firstBody.Links["manifest"])

	secondResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, secondResponse.Code)
	var secondBody v1.Registry
	require.NoError(t, json.Unmarshal(secondResponse.Body.Bytes(), &secondBody))
	assert.Equal(t, second.SchemaVersion, secondResponse.Header().Get("Schema-Version"))
	assert.Equal(t, second.SchemaVersion, secondBody.SchemaVersion)
	assert.Equal(t, second.ManifestDigest, secondBody.ManifestDigest)
	assert.Equal(t, "/v1/releases/1.1.0", secondBody.Links["release"])
}

// TestReleaseRoutesMapPreCommitRepresentationFailure verifies failed response construction returns a safe integrity error instead of committing success.
func TestReleaseRoutesMapPreCommitRepresentationFailure(t *testing.T) {
	manifest := mustTestManifest(t)
	current := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: current.SchemaVersion, ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{current}}
	server := releaseTestServer(snapshot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/v1/registry", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, current.SchemaVersion, response.Header().Get("Schema-Version"))
	assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
}

// TestReleaseCollectionPaginatesControlledSnapshot verifies descending pages retain their original activation ceiling after newer releases appear.
// Items preserve predecessor, provenance, compatibility, and links; invalid or repeated query parameters return bad-request responses.
func TestReleaseCollectionPaginatesControlledSnapshot(t *testing.T) {
	previousVersion, previousDigest := "1.0.0", "sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 2}, releases: []model.Release{
		{SchemaVersion: "2.0.0", ManifestDigest: "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PreviousSchemaVersion: &previousVersion, PreviousManifestDigest: &previousDigest, SourceRepository: "https://git.example.test/repo", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Changes: []byte(`{"compatibility":"additive"}`), ActivationSequence: 2},
		{SchemaVersion: "1.0.0", ManifestDigest: "sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceRepository: "https://git.example.test/repo", SourceCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ActivationSequence: 1},
	}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?limit=1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"schemaVersion":"2.0.0"`)
	assert.Contains(t, response.Body.String(), `"previousRelease":{"schemaVersion":"1.0.0","manifestDigest":"sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	assert.Contains(t, response.Body.String(), `"source":{"repository":"https://git.example.test/repo","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	assert.Contains(t, response.Body.String(), `"compatibility":"additive"`)
	assert.Contains(t, response.Body.String(), `"nextCursor":"`)
	var firstPage v1.Page[v1.Release]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Items, 1)
	assert.Equal(t, v1.Links{
		"self":           "/v1/releases/2.0.0",
		"manifest":       "/v1/releases/2.0.0/manifest",
		"artifacts":      "/v1/releases/2.0.0/artifacts",
		"schemas":        "/v1/releases/2.0.0/schemas",
		"sourceMappings": "/v1/releases/2.0.0/source-mappings",
	}, firstPage.Items[0].Links)
	// Simulate externally supplied later state. The unsigned cursor keeps the
	// second page bound to the original snapshot ceiling of two.
	snapshot.releases = append([]model.Release{{SchemaVersion: "3.0.0", ActivationSequence: 3}}, snapshot.releases...)
	response = httptest.NewRecorder()
	require.NotNil(t, firstPage.NextCursor)
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?limit=1&cursor="+*firstPage.NextCursor, nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"schemaVersion":"1.0.0"`)
	assert.NotContains(t, response.Body.String(), `"schemaVersion":"3.0.0"`)
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?unknown=value"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?limit=1&limit=2"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?limit=0"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?limit=-1"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?limit=not-a-number"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/releases?limit=1001"))
}

// TestReleaseCollectionDefaultAndSingleEntryShapes verifies default limits and single-entry pages produce complete envelopes without unnecessary cursors.
// Initial releases use null predecessor and continuation fields and omit absent compatibility metadata.
func TestReleaseCollectionDefaultAndSingleEntryShapes(t *testing.T) {
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{{SchemaVersion: "1.0.0", ManifestDigest: "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceRepository: "https://git.example.test/repo", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActivationSequence: 1}}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"nextCursor":null`)
	assert.Contains(t, response.Body.String(), `"previousRelease":null`)
	assert.NotContains(t, response.Body.String(), `"compatibility"`)
}

// TestReleaseCollectionSupportsEmptyAndMaximumPages verifies empty collections remain arrays and the maximum page limit is accepted.
func TestReleaseCollectionSupportsEmptyAndMaximumPages(t *testing.T) {
	current := model.Release{SchemaVersion: "1.0.0", ActivationSequence: 1}
	empty := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{current}, page: func(*int64, int64, int) []model.Release { return nil }}
	server := releaseTestServer(empty)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"items":[]`)
	assert.Contains(t, response.Body.String(), `"nextCursor":null`)

	items := make([]model.Release, 1001)
	for index := range items {
		items[index] = model.Release{SchemaVersion: "1.0.0", ActivationSequence: int64(1001 - index)}
	}
	maximum := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1001, SnapshotSequence: 1001}, releases: []model.Release{{SchemaVersion: "1.0.0", ActivationSequence: 1001}}, page: func(_ *int64, _ int64, limit int) []model.Release { return items[:limit] }}
	server = releaseTestServer(maximum)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?limit=1000", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var page v1.Page[v1.Release]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	assert.Len(t, page.Items, 1000)
	assert.NotNil(t, page.NextCursor)
}

// TestReleaseRoutesMapNoActiveAndNotFound verifies unavailable registry state and unknown releases map to distinct public errors.
// No-active-release responses omit version headers; malformed selected versions retain only the resolved current version.
func TestReleaseRoutesMapNoActiveAndNotFound(t *testing.T) {
	server := releaseTestServer(&releaseTestSnapshot{})
	server.releases = release.New(releaseTestReader{err: postgres.ErrNoActiveRelease})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Empty(t, response.Header().Get("Schema-Version"))
	assert.Contains(t, response.Body.String(), `"code":"no-active-release"`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/registry", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Empty(t, response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Body.String())
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Empty(t, response.Header().Get("Schema-Version"))
	assert.Contains(t, response.Body.String(), `"code":"no-active-release"`)

	server = releaseTestServer(&releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{{SchemaVersion: "1.0.0", ActivationSequence: 1}}})
	assert.Equal(t, http.StatusNotFound, serveStatus(server, "/v1/releases/2.0.0"))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/releases/not-a-version", nil))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())
}

// TestReleaseValidationErrorsReportNoActiveRelease verifies missing current activation takes precedence over invalid application parameters.
func TestReleaseValidationErrorsReportNoActiveRelease(t *testing.T) {
	server := releaseTestServer(&releaseTestSnapshot{})
	server.releases = release.New(releaseTestReader{err: postgres.ErrNoActiveRelease})
	for _, test := range []struct {
		name, method, path string
	}{
		{"malformed version", http.MethodHead, "/v1/releases/not-a-version"},
		{"invalid query", http.MethodGet, "/v1/releases?limit=0"},
		{"invalid cursor", http.MethodGet, "/v1/releases?cursor=invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
			assert.Empty(t, response.Header().Get("Schema-Version"))
			if test.method == http.MethodHead {
				assert.Empty(t, response.Body.String())
			} else {
				assert.Contains(t, response.Body.String(), `"code":"no-active-release"`)
			}
		})
	}
	token, err := server.cursors.Encode(pagination.Payload{Resource: "releases", Limit: 100, Sort: "activation-desc", Filters: map[string]string{}, Last: "1", Snapshot: "1"})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?cursor="+token, nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Empty(t, response.Header().Get("Schema-Version"))
	assert.Contains(t, response.Body.String(), `"code":"no-active-release"`)
}

// TestReleaseCollectionAcceptsCursorAfterElapsedTime verifies a valid continuation succeeds without a cursor-expired error.
func TestReleaseCollectionAcceptsCursorAfterElapsedTime(t *testing.T) {
	server := releaseTestServer(&releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{{SchemaVersion: "1.0.0", ActivationSequence: 1}}})
	token, err := server.cursors.Encode(pagination.Payload{Resource: "releases", Limit: 1, Sort: "activation-desc", Filters: map[string]string{}, Last: "1", Snapshot: "1"})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?limit=1&cursor="+token, nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.NotContains(t, response.Body.String(), `"cursor-expired"`)
}

// TestManifestSupportsGzipAndRejectsNoncanonicalBytes verifies negotiated gzip preserves exact canonical content and invalid stored manifests fail before success.
func TestManifestSupportsGzipAndRejectsNoncanonicalBytes(t *testing.T) {
	manifest := mustTestManifest(t)
	release := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, SourceRepository: "https://git.example.test/schema-model", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{release}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/manifest", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, manifest, response.Body.Bytes())
	assert.Equal(t, "application/vnd.apeiro.ocg.schema-release-manifest.v1+json", response.Header().Get("Content-Type"))
	assert.Equal(t, `"`+release.ManifestDigest+`"`, response.Header().Get("ETag"))
	assert.Equal(t, release.ManifestDigest, response.Header().Get("Manifest-Digest"))
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodHead, "/v1/releases/1.0.0/manifest", nil)
	request.Header.Set("Accept-Encoding", "identity;q=0, gzip;q=0")
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotAcceptable, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/manifest", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	server.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	reader, err := gzip.NewReader(response.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, manifest, body)

	release.ManifestBytes = append(release.ManifestBytes, '\n')
	release.ManifestDigest = digest(release.ManifestBytes)
	snapshot.releases = []model.Release{release}
	assert.Equal(t, http.StatusInternalServerError, serveStatus(server, "/v1/releases/1.0.0/manifest"))
	release.ManifestBytes = nil
	release.ManifestDigest = ""
	snapshot.releases = []model.Release{release}
	assert.Equal(t, http.StatusInternalServerError, serveStatus(server, "/v1/releases/1.0.0/manifest"))
}

// TestReleaseHeadErrorsSuppressBodiesAfterNegotiation verifies unacceptable media requests retain resolved version headers and return bodyless HEAD errors.
func TestReleaseHeadErrorsSuppressBodiesAfterNegotiation(t *testing.T) {
	manifest := mustTestManifest(t)
	current := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, ActivationSequence: 1}
	server := releaseTestServer(&releaseTestSnapshot{current: model.CurrentState{SchemaVersion: current.SchemaVersion, ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{current}})
	for _, path := range []string{"/v1/releases/1.0.0", "/v1/releases/1.0.0/manifest"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodHead, path, nil)
		request.Header.Set("Accept", "text/plain")
		server.Handler().ServeHTTP(response, request)
		assert.Equal(t, http.StatusNotAcceptable, response.Code, path)
		assert.Equal(t, current.SchemaVersion, response.Header().Get("Schema-Version"), path)
		assert.Equal(t, current.SchemaVersion, response.Header().Get("Selected-Schema-Version"), path)
		assert.Empty(t, response.Body.String(), path)
	}
}

// TestReleaseDescriptorSupportsHeadAndOptionalFields verifies descriptor HEAD parity and correct inclusion or omission of predecessor and change metadata.
func TestReleaseDescriptorSupportsHeadAndOptionalFields(t *testing.T) {
	initial := mustTestManifest(t)
	currentManifest := versionedManifest(t, initial, "1.0.1", digest(initial))
	previousVersion, previousDigest := "1.0.0", digest(initial)
	current := model.Release{
		SchemaVersion: "1.0.1", ManifestDigest: digest(currentManifest), ManifestBytes: currentManifest,
		PreviousSchemaVersion: &previousVersion, PreviousManifestDigest: &previousDigest,
		SourceRepository: "https://git.example.test/schema-model", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActivationSequence: 2,
	}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, releases: []model.Release{current}}
	server := releaseTestServer(snapshot)

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.1", response.Header().Get("Selected-Schema-Version"))
	var descriptor v1.Release
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &descriptor))
	assert.Equal(t, current.SchemaVersion, descriptor.SchemaVersion)
	assert.Equal(t, current.ManifestDigest, descriptor.ManifestDigest)
	assert.Equal(t, &v1.ReleaseReference{SchemaVersion: previousVersion, ManifestDigest: previousDigest}, descriptor.PreviousRelease)
	assert.Equal(t, v1.ReleaseSource{Repository: current.SourceRepository, Commit: current.SourceCommit}, descriptor.Source)
	assert.Empty(t, descriptor.Compatibility)
	assert.Empty(t, descriptor.Changes)
	assert.Equal(t, v1.Links{
		"self":           "/v1/releases/1.0.1",
		"manifest":       "/v1/releases/1.0.1/manifest",
		"artifacts":      "/v1/releases/1.0.1/artifacts",
		"schemas":        "/v1/releases/1.0.1/schemas",
		"sourceMappings": "/v1/releases/1.0.1/source-mappings",
	}, descriptor.Links)
	tag := response.Header().Get("ETag")

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/releases/1.0.1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, tag, response.Header().Get("ETag"))
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.1", response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())
}

// TestReleaseRoutesHideInactiveRelease verifies stored but unactivated releases remain unavailable through release discovery endpoints.
func TestReleaseRoutesHideInactiveRelease(t *testing.T) {
	manifest := mustTestManifest(t)
	current := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, ActivationSequence: 1}
	inactive := model.Release{SchemaVersion: "2.1.0", ManifestDigest: digest(manifest), ManifestBytes: manifest}
	snapshot := &releaseTestSnapshot{
		current:  model.CurrentState{SchemaVersion: current.SchemaVersion, ActivationSequence: 1, SnapshotSequence: 1},
		releases: []model.Release{current, inactive},
		hidden:   map[string]bool{inactive.SchemaVersion: true},
	}
	server := releaseTestServer(snapshot)
	assert.Equal(t, http.StatusNotFound, serveStatus(server, "/v1/releases/2.1.0"))
	assert.Equal(t, http.StatusNotFound, serveStatus(server, "/v1/releases/2.1.0/manifest"))
}

// TestHistoricalManifestReadsRemainIdenticalForConcurrentCallers verifies simultaneous historical reads return identical immutable bytes and validators.
func TestHistoricalManifestReadsRemainIdenticalForConcurrentCallers(t *testing.T) {
	manifest := mustTestManifest(t)
	release := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{release}}
	server := releaseTestServer(snapshot)
	var group sync.WaitGroup
	errors := make(chan string, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/manifest", nil))
			if response.Code != http.StatusOK || response.Header().Get("Manifest-Digest") != release.ManifestDigest || response.Body.String() != string(manifest) {
				errors <- "manifest response differs"
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

// TestReleaseDescriptorExposesCompleteChangeSummary verifies selected metadata retains the complete authoritative summary and its compatibility classification.
func TestReleaseDescriptorExposesCompleteChangeSummary(t *testing.T) {
	manifest := manifestWithInitialChanges(t, mustTestManifest(t))
	changes := []byte(`{"compatibility":"initial","artifacts":[],"schemaContextBindings":[],"uriCatalog":[],"storageCatalog":[]}`)
	release := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, Changes: changes, ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{release}}
	response := httptest.NewRecorder()
	releaseTestServer(snapshot).Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"compatibility":"initial"`)
	assert.Contains(t, response.Body.String(), `"changes":{"compatibility":"initial","artifacts":[],"schemaContextBindings":[],"uriCatalog":[],"storageCatalog":[]}`)
}

// serveStatus obtains an in-process GET status for route failure assertions.
func serveStatus(server *Server, path string) int {
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response.Code
}

// releaseTestServer wires controlled release state into a server with test admission and readiness settings.
func releaseTestServer(snapshot *releaseTestSnapshot) *Server {
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 64
	return NewWithReleaseService(cfg, &fakeChecker{}, release.New(releaseTestReader{snapshot: snapshot}))
}

// mustTestManifest loads the canonical initial-release fixture and fails the caller if unavailable.
func mustTestManifest(t *testing.T) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "manifest", "valid-initial.json"))
	require.NoError(t, err)
	canonical, err := jcs.Transform(value)
	require.NoError(t, err)
	return canonical
}

// versionedManifest creates canonical release bytes with the requested version and predecessor evidence for pagination fixtures.
func versionedManifest(t *testing.T, input []byte, version, previousDigest string) []byte {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(input, &value))
	value["schemaVersion"] = version
	value["previousRelease"] = map[string]any{"schemaVersion": "1.0.0", "manifestDigest": previousDigest}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	canonical, err := jcs.Transform(raw)
	require.NoError(t, err)
	return canonical
}

// manifestWithInitialChanges adds a complete initial change summary while preserving canonical manifest encoding.
func manifestWithInitialChanges(t *testing.T, input []byte) []byte {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(input, &value))
	value["changes"] = map[string]any{"compatibility": "initial", "artifacts": []any{}, "schemaContextBindings": []any{}, "uriCatalog": []any{}, "storageCatalog": []any{}}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	canonical, err := jcs.Transform(raw)
	require.NoError(t, err)
	return canonical
}

// digest supplies exact-byte SHA-256 integrity metadata for HTTP fixtures.
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
