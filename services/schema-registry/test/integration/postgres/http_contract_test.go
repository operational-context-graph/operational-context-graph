package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// networkResult retains complete network response evidence after the response body closes.
type networkResult struct {
	status int
	header http.Header
	body   []byte
}

// requestNetwork captures a complete optionally conditional response and closes its body for protocol contract assertions.
func requestNetwork(t *testing.T, client *http.Client, method, target, tag string) networkResult {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, target, nil)
	require.NoError(t, err)
	if tag != "" {
		request.Header.Set("If-None-Match", tag)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return networkResult{status: response.StatusCode, header: response.Header, body: body}
}

// boundaryServer connects the production router and use cases to a real PostgreSQL read store with bounded admission.
func boundaryServer(store *storage.Store, checker storage.ReadinessChecker) *httpapi.Server {
	reader := release.NewPostgresReader(store)
	return httpapi.NewWithServices(introspection.Config{
		Database:  introspection.DatabaseConfig{QueryTimeout: 5 * time.Second},
		Admission: introspection.AdmissionConfig{MaxInFlight: 3},
	}, checker, release.New(reader), artifact.New(reader))
}

// TestPopulatedRuntimeHTTPContract verifies runtime-role reads expose exact content, version headers, GET/HEAD validators, redirects, and cross-kind/history pagination over HTTP.
func (s *runtimePoolSuite) TestPopulatedRuntimeHTTPContract() {
	t := s.T()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	database, err := sql.Open("pgx", s.config.URL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	// Restore an empty registry so other runtime-pool tests do not depend on execution order.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, goose.DownContext(cleanup, database, migrationDirectory()))
		require.NoError(t, goose.UpContext(cleanup, database, migrationDirectory()))
	})
	initial := registryFixture{version: "1.0.0", artifacts: []artifactmodel.Artifact{fixtureSchema("device", "device-r1", "1.0.0", []byte("true"), nil)}}
	previous := fixtureRelease(t, initial)
	next := registryFixture{version: "1.1.0", previous: &previous, artifacts: []artifactmodel.Artifact{initial.artifacts[0], fixtureSchema("location", "location-r1", "1.1.0", []byte("false"), nil)}}
	previousNext := fixtureRelease(t, next)
	candidate := candidateFixture(t)
	candidate.previous = &previousNext
	for _, fixture := range []registryFixture{initial, next, candidate} {
		_, loadErr := loadValidatedFixture(t, ctx, database, fixture)
		require.NoError(t, loadErr)
	}
	require.NoError(t, prepopulateReadModelState(ctx, database, candidate.version, map[string]int64{"1.0.0": 1, "1.1.0": 2, candidate.version: 3}))
	pool, err := storage.Open(ctx, s.config)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := pool.Store(storage.Timeouts{Acquire: 5 * time.Second, Query: 5 * time.Second, Statement: 5 * time.Second, Lock: time.Second, IdleInTransaction: 5 * time.Second})
	require.NoError(t, err)
	server := httptest.NewServer(boundaryServer(store, pool).Handler())
	t.Cleanup(server.Close)
	transport := &http.Transport{DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range []string{
		"/v1/registry", "/v1/releases", "/v1/releases/1.1.0", "/v1/releases/1.1.0/manifest",
		"/v1/releases/8.0.0/artifacts", "/v1/releases/8.0.0/schemas/candidate-entity/package",
		"/v1/releases/8.0.0/source-mappings/candidate-mapping-0/package",
		"/v1/artifacts/candidate-mapping-0-r1/resources/shared.bin",
		"/v1/artifacts/device-r1", "/v1/changes?from=1.0.0&to=1.1.0",
	} {
		t.Run(path, func(t *testing.T) {
			baseline := requestNetwork(t, client, http.MethodGet, server.URL+path, "")
			require.Equal(t, http.StatusOK, baseline.status, string(baseline.body))
			assert.Equal(t, "8.0.0", baseline.header.Get("Schema-Version"))
			require.NotEmpty(t, baseline.header.Get("ETag"))
			if strings.HasPrefix(path, "/v1/releases/8.0.0/") {
				assert.Equal(t, "8.0.0", baseline.header.Get("Selected-Schema-Version"))
			}
			head := requestNetwork(t, client, http.MethodHead, server.URL+path, "")
			assert.Equal(t, http.StatusOK, head.status)
			assert.Empty(t, head.body)
			assert.Equal(t, baseline.header.Get("ETag"), head.header.Get("ETag"))
			conditional := requestNetwork(t, client, http.MethodGet, server.URL+path, baseline.header.Get("ETag"))
			assert.Equal(t, http.StatusNotModified, conditional.status)
			assert.Empty(t, conditional.body)
			if path == "/v1/artifacts/device-r1" {
				assert.Equal(t, []byte("true"), baseline.body)
			}
			if strings.HasSuffix(path, "/resources/shared.bin") {
				assert.Equal(t, []byte{0, 255, 1}, baseline.body)
			}
		})
	}
	resolve := server.URL + "/v1/releases/8.0.0/resolve?uri=" + url.QueryEscape(candidate.artifacts[0].CanonicalURI)
	redirect := requestNetwork(t, client, http.MethodGet, resolve, "")
	require.Equal(t, http.StatusTemporaryRedirect, redirect.status)
	assert.Equal(t, "/v1/artifacts/candidate-edge-r1", redirect.header.Get("Location"))
	assert.Equal(t, http.StatusNotModified, requestNetwork(t, client, http.MethodGet, resolve, redirect.header.Get("ETag")).status)

	// Traverse every artifact, including the schema-to-mapping kind boundary.
	path := "/v1/releases/8.0.0/artifacts?limit=1"
	var identities []string
	for index := 0; ; index++ {
		require.Less(t, index, len(candidate.artifacts))
		response := requestNetwork(t, client, http.MethodGet, server.URL+path, "")
		require.Equal(t, http.StatusOK, response.status, string(response.body))
		var page v1.Page[v1.ArtifactDescriptor]
		require.NoError(t, json.Unmarshal(response.body, &page))
		require.Len(t, page.Items, 1)
		identities = append(identities, page.Items[0].ArtifactRevision)
		if page.NextCursor == nil {
			break
		}
		path = "/v1/releases/8.0.0/artifacts?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	assert.Equal(t, []string{"candidate-edge-r1", "candidate-entity-r1", "candidate-source-r1", "candidate-mapping-0-r1", "candidate-mapping-1-r1"}, identities)
	first := requestNetwork(t, client, http.MethodGet, server.URL+"/v1/artifact-kinds/schema/device/history?limit=1", "")
	require.Equal(t, http.StatusOK, first.status, string(first.body))
	var history v1.Page[v1.ArtifactHistoryItem]
	require.NoError(t, json.Unmarshal(first.body, &history))
	require.Len(t, history.Items, 1)
	assert.Equal(t, "1.1.0", history.Items[0].SchemaVersion)
	require.NotNil(t, history.NextCursor)
	last := requestNetwork(t, client, http.MethodGet, server.URL+"/v1/artifact-kinds/schema/device/history?limit=1&cursor="+url.QueryEscape(*history.NextCursor), "")
	require.Equal(t, http.StatusOK, last.status, string(last.body))
	require.NoError(t, json.Unmarshal(last.body, &history))
	require.Len(t, history.Items, 1)
	assert.Equal(t, "1.0.0", history.Items[0].SchemaVersion)
	assert.Nil(t, history.NextCursor)
}

// TestPackagePageRejectsCorruptLaterProgramOverHTTP verifies a later corrupt program defeats conditional success and rejects the whole page without items or validators.
// The unaffected first mapping remains readable after the failed transaction.
func (s *readStoreSuite) TestPackagePageRejectsCorruptLaterProgramOverHTTP() {
	t := s.T()
	store := s.candidateStore()
	server := httptest.NewServer(boundaryServer(store, nil).Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	target := server.URL + "/v1/releases/8.0.0/schemas/candidate-source/source-mappings/packages?limit=2"
	baseline := requestNetwork(t, client, http.MethodGet, target, "")
	require.Equal(t, http.StatusOK, baseline.status, string(baseline.body))
	var page v1.Page[v1.SourceMappingPackage]
	require.NoError(t, json.Unmarshal(baseline.body, &page))
	require.Len(t, page.Items, 2)
	tx, err := s.Database.BeginTx(s.Context, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(s.Context, "SET LOCAL session_replication_role = replica")
	require.NoError(t, err)
	result, err := tx.ExecContext(s.Context, `UPDATE schema_registry.artifact_resources SET bytes = $1 WHERE artifact_revision = $2 AND name = $3`, []byte{9, 9, 9}, "candidate-mapping-1-r1", "shared.bin")
	require.NoError(t, err)
	count, err := result.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, tx.Commit())
	response := requestNetwork(t, client, http.MethodGet, target, baseline.header.Get("ETag"))
	require.Equal(t, http.StatusInternalServerError, response.status, string(response.body))
	assert.Equal(t, "8.0.0", response.header.Get("Schema-Version"))
	assert.Equal(t, "8.0.0", response.header.Get("Selected-Schema-Version"))
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.body, &envelope))
	assert.Equal(t, "inconsistent-registry-state", envelope.Error.Code)
	assert.NotContains(t, string(response.body), `"items"`)
	assert.NotContains(t, string(response.body), "candidate-mapping-0")
	assert.Empty(t, response.header.Get("ETag"))
	// The unaffected first mapping still works after the failed multi-package read.
	first := requestNetwork(t, client, http.MethodGet, server.URL+"/v1/releases/8.0.0/source-mappings/candidate-mapping-0/package", "")
	assert.Equal(t, http.StatusOK, first.status, string(first.body))
}
