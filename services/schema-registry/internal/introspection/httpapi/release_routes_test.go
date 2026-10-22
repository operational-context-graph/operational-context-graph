// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
)

type releaseTestReader struct {
	snapshot *releaseTestSnapshot
	err      error
}

func (r releaseTestReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(ctx, r.snapshot)
}

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

func (s *releaseTestSnapshot) CurrentState() model.CurrentState { return s.current }
func (s *releaseTestSnapshot) CurrentRelease(context.Context) (model.Release, error) {
	result, err := s.Release(context.Background(), s.current.SchemaVersion)
	if s.afterCurrentRelease != nil {
		after := s.afterCurrentRelease
		s.afterCurrentRelease = nil
		after()
	}
	return result, err
}
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
func (s *releaseTestSnapshot) IsAncestor(context.Context, string, string) (bool, error) {
	if s.lineageErr != nil {
		return false, s.lineageErr
	}
	if s.ancestor == nil {
		return true, nil
	}
	return *s.ancestor, nil
}
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
func (*releaseTestSnapshot) ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error) {
	return model.Artifact{}, postgres.ErrNotFound
}
func (*releaseTestSnapshot) ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) Resources(context.Context, string) ([]model.Resource, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) Resource(context.Context, string, string) (model.Resource, error) {
	return model.Resource{}, postgres.ErrNotFound
}
func (*releaseTestSnapshot) Dependencies(context.Context, string) ([]model.Dependency, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) ContextBindings(context.Context, string, string) ([]model.ContextBinding, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) StorageEntries(context.Context, string, string) ([]model.StorageEntry, error) {
	return nil, postgres.ErrNotFound
}
func (*releaseTestSnapshot) URIEntry(context.Context, string, string) (model.URIEntry, error) {
	return model.URIEntry{}, postgres.ErrNotFound
}
func (*releaseTestSnapshot) SourceMapping(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}
func (*releaseTestSnapshot) SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

func TestReleaseDiscoveryAndManifestConditionals(t *testing.T) {
	manifest := mustTestManifest(t)
	release := model.Release{SchemaVersion: "1.0.0", ManifestDigest: digest(manifest), ManifestBytes: manifest, SourceRepository: "https://git.example.test/schema-model", SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActivationSequence: 1}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}, releases: []model.Release{release}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	var body registryResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "1.0.0", body.SchemaVersion)
	assert.Equal(t, release.ManifestDigest, body.ManifestDigest)
	assert.Equal(t, links{
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
	server.discoveryLinks = func(version string) links {
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
	var firstBody registryResponse
	require.NoError(t, json.Unmarshal(firstResponse.Body.Bytes(), &firstBody))
	assert.Equal(t, first.SchemaVersion, firstResponse.Header().Get("Schema-Version"))
	assert.Equal(t, first.SchemaVersion, firstBody.SchemaVersion)
	assert.Equal(t, first.ManifestDigest, firstBody.ManifestDigest)
	assert.Equal(t, "/v1/releases/1.0.0", firstBody.Links["release"])
	assert.Equal(t, "/v1/releases/1.0.0/manifest", firstBody.Links["manifest"])

	secondResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, "/v1/registry", nil))
	require.Equal(t, http.StatusOK, secondResponse.Code)
	var secondBody registryResponse
	require.NoError(t, json.Unmarshal(secondResponse.Body.Bytes(), &secondBody))
	assert.Equal(t, second.SchemaVersion, secondResponse.Header().Get("Schema-Version"))
	assert.Equal(t, second.SchemaVersion, secondBody.SchemaVersion)
	assert.Equal(t, second.ManifestDigest, secondBody.ManifestDigest)
	assert.Equal(t, "/v1/releases/1.1.0", secondBody.Links["release"])
}

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
	assert.Contains(t, response.Body.String(), `"previousRelease":{"manifestDigest":"sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","schemaVersion":"1.0.0"}`)
	assert.Contains(t, response.Body.String(), `"source":{"repository":"https://git.example.test/repo","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	assert.Contains(t, response.Body.String(), `"compatibility":"additive"`)
	assert.Contains(t, response.Body.String(), `"nextCursor":"`)
	var firstPage struct {
		Items      []releaseResponse `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Items, 1)
	assert.Equal(t, links{
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
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases?limit=1&cursor="+firstPage.NextCursor, nil))
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
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	assert.Len(t, page.Items, 1000)
	assert.NotNil(t, page.NextCursor)
}

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
	var descriptor releaseResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &descriptor))
	assert.Equal(t, current.SchemaVersion, descriptor.SchemaVersion)
	assert.Equal(t, current.ManifestDigest, descriptor.ManifestDigest)
	assert.Equal(t, map[string]any{"schemaVersion": previousVersion, "manifestDigest": previousDigest}, descriptor.PreviousRelease)
	assert.Equal(t, sourceResponse{Repository: current.SourceRepository, Commit: current.SourceCommit}, descriptor.Source)
	assert.Empty(t, descriptor.Compatibility)
	assert.Empty(t, descriptor.Changes)
	assert.Equal(t, links{
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

func TestChangesValidatesParametersAndSupportsConditionalResponse(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	artifacts := map[string]model.Artifact{"rev-device-v1": {Revision: "rev-device-v1", Payload: model.ByteDescriptor{MediaType: "application/schema+json", Bytes: []byte("true")}}}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, artifacts: artifacts, releases: []model.Release{
		{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2},
		{SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1},
	}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"fromSchemaVersion":"1.0.0"`)
	assert.Contains(t, response.Body.String(), `"toSchemaVersion":"1.0.1"`)
	assert.Contains(t, response.Body.String(), `"compatibility":`)
	assert.Contains(t, response.Body.String(), `"artifacts":`)
	assert.Contains(t, response.Body.String(), `"schemaContextBindings":`)
	assert.Contains(t, response.Body.String(), `"uriCatalog":`)
	assert.Contains(t, response.Body.String(), `"storageCatalog":`)
	assert.Contains(t, response.Body.String(), `"from":"/v1/releases/1.0.0"`)
	assert.Contains(t, response.Body.String(), `"to":"/v1/releases/1.0.1"`)
	tag := response.Header().Get("ETag")
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil)
	request.Header.Set("If-None-Match", tag)
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotModified, response.Code)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, tag, response.Header().Get("ETag"))
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Body.String())
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0&from=1.0.1&to=1.0.1"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=&to=1.0.1"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=not-a-version&to=1.0.1"))
}

func TestChangesMapsReleaseAndStorageFailures(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, releases: []model.Release{
		{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2},
		{SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1},
	}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/9.0.0", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/changes?from=1.0.0&to=9.0.0", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0&to=1.0.0"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.1&to=1.0.0"))
	disconnected := false
	snapshot.ancestor = &disconnected
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"versions-not-comparable"`)
	snapshot.ancestor = nil
	snapshot.lineageErr = postgres.ErrInconsistentState
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	snapshot.lineageErr = nil

	server.releases = release.New(releaseTestReader{err: errors.New("database unavailable")})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"registry-unavailable"`)
}

func TestChangesReadsControlledSnapshotConcurrently(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, artifacts: map[string]model.Artifact{"fixture": {Revision: "fixture", Payload: model.ByteDescriptor{MediaType: "application/schema+json", Bytes: []byte("true")}}}, releases: []model.Release{{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2}, {SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1}}}
	server := releaseTestServer(snapshot)
	var group sync.WaitGroup
	results := make(chan struct {
		body, tag string
		status    int
	}, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
			results <- struct {
				body, tag string
				status    int
			}{response.Body.String(), response.Header().Get("ETag"), response.Code}
		}()
	}
	group.Wait()
	close(results)
	var firstResult struct {
		body, tag string
		status    int
	}
	for result := range results {
		if firstResult.tag == "" {
			firstResult = result
		}
		assert.Equal(t, http.StatusOK, result.status)
		assert.Equal(t, firstResult.body, result.body)
		assert.Equal(t, firstResult.tag, result.tag)
	}
}

func serveStatus(server *Server, path string) int {
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response.Code
}

func releaseTestServer(snapshot *releaseTestSnapshot) *Server {
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 64
	return NewWithReleaseService(cfg, &fakeChecker{}, release.New(releaseTestReader{snapshot: snapshot}))
}
func mustTestManifest(t *testing.T) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "..", "schema-artifacts", "manifest", "testdata", "valid-initial.json"))
	require.NoError(t, err)
	canonical, err := jcs.Transform(value)
	require.NoError(t, err)
	return canonical
}
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
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
