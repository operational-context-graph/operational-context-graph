// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

func TestSchemaRoutesMetadataContentContextsPackageAndStores(t *testing.T) {
	server := artifactTestServer(schemaFixture())

	t.Run("metadata and collection expose release links", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"artifactType":"device"`)
		assert.Contains(t, response.Body.String(), `"contexts":"/v1/releases/1.0.0/schemas/device/contexts"`)

		response = serveArtifactRequest(server, http.MethodHead, "/v1/releases/1.0.0/schemas/device")
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Empty(t, response.Body.String())
		assert.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	})

	t.Run("content preserves exact bytes and supports gzip validators", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/content")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, `{"type":"object"}`, response.Body.String())
		assert.Equal(t, "application/schema+json", response.Header().Get("Content-Type"))
		tag := response.Header().Get("ETag")

		request := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/content", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		reader, err := gzip.NewReader(response.Body)
		require.NoError(t, err)
		bytes, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Equal(t, `{"type":"object"}`, string(bytes))
		assert.NotEqual(t, tag, response.Header().Get("ETag"))
	})

	t.Run("contexts and package preserve complete parsed values", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"schemaVersion":"1.0.0"`)
		assert.Contains(t, response.Body.String(), `"rev-context"`)

		response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"document":{"type":"object"}`)
		assert.Contains(t, response.Body.String(), `"document":{"@context":{}}`)
	})

	t.Run("stores expose logical assignments only", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/stores")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"stores":["data-center-inventory"]`)
		assert.NotContains(t, response.Body.String(), "postgres")
	})
}

func TestSchemaRoutesRejectInvalidAndInconsistentData(t *testing.T) {
	t.Run("unknown schema and pagination contexts fail", func(t *testing.T) {
		server := artifactTestServer(schemaFixture())
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/missing")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"schema-not-in-release"`)
		response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/missing/package")
		assert.Equal(t, http.StatusNotFound, response.Code)
		response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts?limit=1")
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("wrong schema media type and missing canonical stores are inconsistent", func(t *testing.T) {
		fixture := schemaFixture()
		invalid := fixture.selections["schema/device"]
		invalid.Payload.MediaType = "application/json"
		fixture.selections["schema/device"] = invalid
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/content")
		assert.Equal(t, http.StatusInternalServerError, response.Code)

		fixture = schemaFixture()
		fixture.stores = map[string][]model.StorageEntry{}
		response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/stores")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
	})
}

func TestBoundContextsCoverEmptySeveralMissingAndDuplicateBindings(t *testing.T) {
	t.Run("no contexts returns complete empty wrapper", func(t *testing.T) {
		fixture := schemaFixture()
		fixture.bindings = map[string][]model.ContextBinding{}
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"schemaVersion":"1.0.0"`)
		assert.Contains(t, response.Body.String(), `"artifactType":"device"`)
		assert.Contains(t, response.Body.String(), `"artifactRevision":"rev-schema"`)
		assert.Contains(t, response.Body.String(), `"items":[]`)
		assert.Contains(t, response.Body.String(), `"self":"/v1/releases/1.0.0/schemas/device/contexts"`)
	})

	t.Run("several contexts remain ordered and complete", func(t *testing.T) {
		fixture := schemaFixture()
		second := fixture.selections["context/device"]
		second.Revision = "rev-context-two"
		fixture.artifacts[second.Revision] = second
		fixture.resources[second.Revision], fixture.dependencies[second.Revision] = nil, nil
		fixture.bindings["1.0.0\x00rev-schema"] = append(fixture.bindings["1.0.0\x00rev-schema"], model.ContextBinding{SchemaVersion: "1.0.0", SchemaRevision: "rev-schema", ContextRevision: second.Revision})
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"rev-context-two"`)
	})

	t.Run("missing or duplicate binding is inconsistent", func(t *testing.T) {
		fixture := schemaFixture()
		fixture.bindings["1.0.0\x00rev-schema"] = []model.ContextBinding{{SchemaVersion: "1.0.0", SchemaRevision: "rev-schema", ContextRevision: "missing"}}
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		fixture = schemaFixture()
		binding := fixture.bindings["1.0.0\x00rev-schema"][0]
		fixture.bindings["1.0.0\x00rev-schema"] = append(fixture.bindings["1.0.0\x00rev-schema"], binding)
		response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
	})
}

func TestBoundContextsConcurrentCallersReceiveOneCompleteSet(t *testing.T) {
	server := artifactTestServer(schemaFixture())
	const callers = 24
	results := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/contexts")
			if response.Code == http.StatusOK {
				results <- response.Body.String()
			}
		}()
	}
	group.Wait()
	close(results)
	var first string
	for value := range results {
		if first == "" {
			first = value
		}
		assert.Equal(t, first, value)
		assert.Equal(t, 1, strings.Count(value, `"artifactRevision":"rev-context"`))
	}
	require.NotEmpty(t, first)
}

func TestSchemaPackageConcurrentReadsAreStable(t *testing.T) {
	server := artifactTestServer(schemaFixture())
	const callers = 32
	bodies := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
			if response.Code == http.StatusOK {
				bodies <- response.Body.String()
			}
		}()
	}
	group.Wait()
	close(bodies)
	var first string
	for body := range bodies {
		if first == "" {
			first = body
		}
		assert.Equal(t, first, body)
	}
	require.NotEmpty(t, first)
}

func TestSchemaPackageSupportsBooleanSchemas(t *testing.T) {
	fixture := schemaFixture()
	value := fixture.selections["schema/device"]
	value.Payload.Bytes = []byte("true")
	value.Payload.Size = 4
	value.Payload.Digest = fixtureBytes([]byte("true"), "application/schema+json").Digest
	fixture.selections["schema/device"] = value
	fixture.artifacts[value.Revision] = value
	fixture.collections["1.0.0"][0] = value
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
	require.Equal(t, http.StatusOK, response.Code)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.True(t, strings.Contains(string(body["schema"]), `"document":true`))
}

func TestSchemaPackageNegotiationConditionalsAndInvalidContextFailAtomically(t *testing.T) {
	server := artifactTestServer(schemaFixture())
	request := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/package", nil)
	request.Header.Set("Accept", "text/plain")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotAcceptable, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/package", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	tag := response.Header().Get("ETag")
	request.Header.Set("If-None-Match", tag)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotModified, response.Code)
	assert.Empty(t, response.Body.String())

	fixture := schemaFixture()
	context := fixture.artifacts["rev-context"]
	context.Payload.Bytes = []byte(`{`)
	context.Payload.Size = 1
	context.Payload.Digest = fixtureBytes(context.Payload.Bytes, context.Payload.MediaType).Digest
	fixture.artifacts[context.Revision] = context
	response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), `"schema":`)
}

func TestSchemaPackageHandlesZeroAndSeveralContexts(t *testing.T) {
	fixture := schemaFixture()
	fixture.bindings = map[string][]model.ContextBinding{}
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"contexts":[]`)

	fixture = schemaFixture()
	second := fixture.artifacts["rev-context"]
	second.Revision = "rev-context-second"
	fixture.artifacts[second.Revision] = second
	fixture.bindings["1.0.0\x00rev-schema"] = append(fixture.bindings["1.0.0\x00rev-schema"], model.ContextBinding{SchemaVersion: "1.0.0", SchemaRevision: "rev-schema", ContextRevision: second.Revision})
	response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 2, strings.Count(response.Body.String(), `"document":{"@context":{}}`))
}

func TestSchemaPackageRejectsCorruptionAndDependencyFailuresWithoutPartialBody(t *testing.T) {
	t.Run("corrupt schema descriptor", func(t *testing.T) {
		fixture := schemaFixture()
		schema := fixture.selections["schema/device"]
		schema.Payload.Size++
		fixture.selections["schema/device"] = schema
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), `"schema":`)
	})
	t.Run("dependency query failure", func(t *testing.T) {
		fixture := schemaFixture()
		fixture.dependencyErr = postgres.ErrInconsistentState
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/package")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), `"schema":`)
	})
}

func TestSchemaContentBooleanConditionalAndHistoricalReads(t *testing.T) {
	fixture := schemaFixture()
	value := fixture.selections["schema/device"]
	value.Payload.Bytes = []byte("false")
	value.Payload.Size = int64(len(value.Payload.Bytes))
	value.Payload.Digest = fixtureBytes(value.Payload.Bytes, value.Payload.MediaType).Digest
	fixture.selections["schema/device"], fixture.artifacts[value.Revision] = value, value
	fixture.collections["1.0.0"][0] = value
	fixture.releases["1.1.0"] = model.Release{SchemaVersion: "1.1.0", ActivationSequence: 2}
	server := artifactTestServer(fixture)

	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/content")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "false", response.Body.String())
	tag := response.Header().Get("ETag")
	request := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/content", nil)
	request.Header.Set("If-None-Match", tag)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotModified, response.Code)
	assert.Empty(t, response.Body.String())

	const callers = 24
	var group sync.WaitGroup
	statuses := make(chan int, callers)
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses <- serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.1.0/schemas/device/content").Code
		}()
	}
	group.Wait()
	close(statuses)
	for status := range statuses {
		assert.Equal(t, http.StatusOK, status)
	}
}

func TestSchemaContentRejectsCorruptDescriptor(t *testing.T) {
	fixture := schemaFixture()
	value := fixture.selections["schema/device"]
	value.Payload.Size++
	fixture.selections["schema/device"] = value
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/content")
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
}

func TestSchemaCollectionPaginatesAndFiltersExactValues(t *testing.T) {
	fixture := schemaFixture()
	class := "entity-type"
	items := make([]model.Artifact, 0, 1001)
	for index := 0; index < 1001; index++ {
		typeName := fmt.Sprintf("schema-%04d", index)
		items = append(items, fixtureArtifact(fmt.Sprintf("revision-%04d", index), "schema", typeName, &class, nil, []byte(`true`)))
		items[index].Payload.MediaType = "application/schema+json"
	}
	fixture.collections["1.0.0"] = items
	server := artifactTestServer(fixture)
	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas?schemaClass=entity-type&limit=1000")
	require.Equal(t, http.StatusOK, response.Code)
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Items, 1000)
	require.NotNil(t, page.NextCursor)
	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas?schemaClass=entity-type&limit=1000&cursor="+url.QueryEscape(*page.NextCursor))
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	assert.Len(t, page.Items, 1)
	assert.Nil(t, page.NextCursor)

	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas?schemaClass=unknown")
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas?dependsOnRevision=bad/path")
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestSchemaCollectionSupportsEmptyOneItemAndExactDependencyFilters(t *testing.T) {
	fixture := schemaFixture()
	fixture.collections["1.0.0"] = nil
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"items":[]`)
	assert.Contains(t, response.Body.String(), `"nextCursor":null`)

	fixture = schemaFixture()
	fixture.dependencies["rev-schema"] = []model.Dependency{{ArtifactRevision: "rev-schema", Kind: "context", ArtifactType: "device", Revision: "rev-context"}}
	response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas?schemaClass=entity-type&dependsOnRevision=rev-context")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"artifactType":"device"`)

	response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas?schemaClass=source-record&dependsOnRevision=rev-context")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"items":[]`)
}

func TestSchemaCollectionConcurrentReadSnapshotsRemainIndependent(t *testing.T) {
	server := artifactTestServer(schemaFixture())
	const callers = 32
	results := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas")
			if response.Code == http.StatusOK {
				results <- response.Body.String()
			}
		}()
	}
	group.Wait()
	close(results)
	var first string
	for result := range results {
		if first == "" {
			first = result
		}
		assert.Equal(t, first, result)
	}
	require.NotEmpty(t, first)
}

func TestSchemaStoresSupportSourceRecordsAndSeveralSortedStores(t *testing.T) {
	fixture := schemaFixture()
	fixture.stores["1.0.0\x00device"] = []model.StorageEntry{{Store: "telemetry-store"}, {Store: "data-center-inventory"}}
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/stores")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"stores":["data-center-inventory","telemetry-store"]`)

	fixture = schemaFixture()
	source := fixture.selections["schema/device"]
	class := "source-record"
	source.SchemaClass = &class
	fixture.selections["schema/device"], fixture.artifacts[source.Revision] = source, source
	fixture.stores["1.0.0\x00device"] = nil
	response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/schemas/device/stores")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"stores":[]`)
}

func TestSchemaStoresAbsentAndConcurrentCallers(t *testing.T) {
	t.Run("absent schema", func(t *testing.T) {
		response := serveArtifactRequest(artifactTestServer(schemaFixture()), http.MethodGet, "/v1/releases/1.0.0/schemas/missing/stores")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"schema-not-in-release"`)
	})
	t.Run("concurrent callers receive sorted logical stores only", func(t *testing.T) {
		fixture := schemaFixture()
		fixture.stores["1.0.0\x00device"] = []model.StorageEntry{{Store: "telemetry-store"}, {Store: "data-center-inventory"}}
		server := artifactTestServer(fixture)
		const callers = 24
		results := make(chan string, callers)
		var group sync.WaitGroup
		for range callers {
			group.Add(1)
			go func() {
				defer group.Done()
				response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/device/stores")
				if response.Code == http.StatusOK {
					results <- response.Body.String()
				}
			}()
		}
		group.Wait()
		close(results)
		var first string
		for value := range results {
			if first == "" {
				first = value
			}
			assert.Equal(t, first, value)
			assert.Contains(t, value, `"stores":["data-center-inventory","telemetry-store"]`)
			assert.NotContains(t, value, "credential")
		}
		require.NotEmpty(t, first)
	})
}

func TestSourceMappingCollectionRejectsUnknownSchemasAndEmptyFilters(t *testing.T) {
	server := artifactTestServer(schemaFixture())
	for _, path := range []string{
		"/v1/releases/1.0.0/source-mappings?sourceArtifactType=missing",
		"/v1/releases/1.0.0/source-mappings?targetArtifactType=missing",
		"/v1/releases/1.0.0/source-mappings?engineLanguage=",
		"/v1/releases/1.0.0/source-mappings?executionProfile=",
	} {
		response := serveArtifactRequest(server, http.MethodGet, path)
		if strings.Contains(path, "missing") {
			assert.Equal(t, http.StatusNotFound, response.Code, path)
		} else {
			assert.Equal(t, http.StatusBadRequest, response.Code, path)
		}
	}
}

func TestSourceMappingCollectionFiltersEnvelopeAndConcurrentReads(t *testing.T) {
	server := artifactTestServer(mappingFixture())
	path := "/v1/releases/1.0.0/source-mappings?sourceArtifactType=device&targetArtifactType=device&engineLanguage=arbitrary-engine&executionProfile=arbitrary-profile"
	response := serveArtifactRequest(server, http.MethodGet, path)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"schemaVersion":"1.0.0"`)
	assert.Contains(t, response.Body.String(), `"nextCursor":null`)
	assert.Contains(t, response.Body.String(), `"language":"arbitrary-engine"`)
	assert.Contains(t, response.Body.String(), `"self":"/v1/releases/1.0.0/source-mappings?`)

	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?engineLanguage=other")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"items":[]`)

	const callers = 24
	results := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result := serveArtifactRequest(server, http.MethodGet, path)
			if result.Code == http.StatusOK {
				results <- result.Body.String()
			}
		}()
	}
	group.Wait()
	close(results)
	var first string
	for result := range results {
		if first == "" {
			first = result
		}
		assert.Equal(t, first, result)
	}
	require.NotEmpty(t, first)
}

func TestSourceMappingCollectionPaginatesAndAcceptsSharedSources(t *testing.T) {
	t.Run("1001 mappings paginate through an unsigned cursor", func(t *testing.T) {
		fixture := mappingFixture()
		items := make([]model.Artifact, 0, 1001)
		for index := 0; index < 1001; index++ {
			typeName := fmt.Sprintf("mapping-%04d", index)
			revision := fmt.Sprintf("mapping-revision-%04d", index)
			item := fixtureArtifact(revision, "source-mapping", typeName, nil, stringPointer("1.0.0"), []byte(`{}`))
			items = append(items, item)
			fixture.artifacts[revision] = item
			detail := fixture.mappings["rev-mapping"]
			detail.MappingRevision, detail.EngineLanguage, detail.ExecutionProfile = revision, "publisher", "default"
			fixture.resources[revision] = append([]model.Resource(nil), fixture.resources["rev-mapping"]...)
			setMappingFixture(fixture, item, detail)
		}
		fixture.collections["1.0.0"] = items
		server := artifactTestServer(fixture)
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?limit=1000")
		require.Equal(t, http.StatusOK, response.Code)
		var page struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor *string           `json:"nextCursor"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		require.Len(t, page.Items, 1000)
		require.NotNil(t, page.NextCursor)
		response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?limit=1000&cursor="+url.QueryEscape(*page.NextCursor))
		require.Equal(t, http.StatusOK, response.Code)
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		assert.Len(t, page.Items, 1)
		assert.Nil(t, page.NextCursor)
	})

	t.Run("shared resolved source is valid", func(t *testing.T) {
		fixture := mappingFixture()
		original := fixture.selections["source-mapping/device-mapping"]
		duplicate := original
		duplicate.Revision, duplicate.Type = "rev-mapping-two", "device-mapping-two"
		fixture.collections["1.0.0"] = append(fixture.collections["1.0.0"], duplicate)
		fixture.artifacts[duplicate.Revision] = duplicate
		fixture.resources[duplicate.Revision] = append([]model.Resource(nil), fixture.resources[original.Revision]...)
		detail := fixture.mappings[original.Revision]
		detail.MappingRevision = duplicate.Revision
		setMappingFixture(fixture, duplicate, detail)
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings")
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"artifactType":"device-mapping-two"`)
	})
}

func TestSourceMappingCursorUsesLastMatchingFilteredItem(t *testing.T) {
	fixture := mappingFixture()
	items := make([]model.Artifact, 0, 1002)
	for index := 0; index < 1002; index++ {
		typeName := fmt.Sprintf("mapping-filter-%04d", index)
		revision := fmt.Sprintf("mapping-filter-revision-%04d", index)
		item := fixtureArtifact(revision, "source-mapping", typeName, nil, stringPointer("1.0.0"), []byte(`{}`))
		items = append(items, item)
		fixture.artifacts[revision] = item
		language := "skip"
		if index >= 1000 {
			language = "keep"
		}
		detail := fixture.mappings["rev-mapping"]
		detail.MappingRevision, detail.EngineLanguage = revision, language
		fixture.resources[revision] = append([]model.Resource(nil), fixture.resources["rev-mapping"]...)
		setMappingFixture(fixture, item, detail)
	}
	fixture.collections["1.0.0"] = items
	server := artifactTestServer(fixture)
	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?engineLanguage=keep&limit=1")
	require.Equal(t, http.StatusOK, response.Code)
	var page struct {
		Items []struct {
			ArtifactType string `json:"artifactType"`
		} `json:"items"`
		NextCursor *string `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "mapping-filter-1000", page.Items[0].ArtifactType)
	require.NotNil(t, page.NextCursor)
	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?engineLanguage=keep&limit=1&cursor="+url.QueryEscape(*page.NextCursor))
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "mapping-filter-1001", page.Items[0].ArtifactType)
	assert.Nil(t, page.NextCursor)
}

func TestSourceMappingFiltersUseResolvedDependenciesNotMappingIdentity(t *testing.T) {
	fixture := mappingFixture()
	mapping := fixture.selections["source-mapping/device-mapping"]
	mapping.Type = "publisher-descriptor-name"
	setMappingFixture(fixture, mapping, fixture.mappings[mapping.Revision])
	fixture.collections["1.0.0"] = []model.Artifact{mapping}
	fixture.artifacts[mapping.Revision] = mapping
	server := artifactTestServer(fixture)

	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings?sourceArtifactType=device")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"artifactType":"publisher-descriptor-name"`)
	assert.Contains(t, response.Body.String(), `"sourceSchema":{"artifactRevision":"rev-schema"`)
}

func TestSourceMappingRoutesPreserveOpaqueProgramMetadata(t *testing.T) {
	server := artifactTestServer(mappingFixture())

	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"language":"arbitrary-engine"`)
	assert.Contains(t, response.Body.String(), `"href":"/v1/artifacts/rev-mapping/resources/program.bin"`)

	response = serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"content":"b3BhcXVlAHByb2dyYW0="`)
	assert.NotContains(t, response.Body.String(), "execute")
}

func TestSourceSchemaMappingLookupErrorsIntegrityAndConditionals(t *testing.T) {
	t.Run("absent schema and mapping have distinct failures", func(t *testing.T) {
		server := artifactTestServer(mappingFixture())
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/missing/source-mappings")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"schema-not-in-release"`)
		fixture := mappingFixture()
		fixture.mappings = map[string]model.SourceMappingDetail{}
		delete(fixture.selections, "source-mapping/device-mapping")
		response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"artifact-not-in-release"`)
	})

	t.Run("program mismatch is inconsistent and validators are conditional", func(t *testing.T) {
		server := artifactTestServer(mappingFixture())
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping")
		require.Equal(t, http.StatusOK, response.Code)
		request := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping", nil)
		request.Header.Set("If-None-Match", response.Header().Get("ETag"))
		conditional := httptest.NewRecorder()
		server.Handler().ServeHTTP(conditional, request)
		assert.Equal(t, http.StatusNotModified, conditional.Code)

		fixture := mappingFixture()
		detail := fixture.mappings["rev-mapping"]
		detail.Transformations = append([]model.MappingTransformation(nil), detail.Transformations...)
		detail.Transformations[0].ProgramDigest = fixtureBytes([]byte("different"), "application/vnd.example.program").Digest
		fixture.mappings["rev-mapping"] = detail
		response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
	})
}

func TestSourceMappingPackageNegotiationAndConcurrentReads(t *testing.T) {
	server := artifactTestServer(mappingFixture())
	request := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package", nil)
	request.Header.Set("Accept", "text/plain")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotAcceptable, response.Code)

	request = httptest.NewRequest(http.MethodHead, "/v1/releases/1.0.0/source-mappings/device-mapping/package", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Empty(t, response.Body.String())

	const callers = 32
	bodies := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
			if result.Code == http.StatusOK {
				bodies <- result.Body.String()
			}
		}()
	}
	group.Wait()
	close(bodies)
	var first string
	for body := range bodies {
		if first == "" {
			first = body
		}
		assert.Equal(t, first, body)
	}
	require.NotEmpty(t, first)
}

func TestSourceMappingPackageConcurrentCallersReceiveOneReleaseSnapshot(t *testing.T) {
	server := artifactTestServer(mappingFixture())
	const callers = 32
	results := make(chan []byte, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
			if response.Code == http.StatusOK {
				results <- response.Body.Bytes()
			}
		}()
	}
	group.Wait()
	close(results)
	var first []byte
	for value := range results {
		var body struct {
			SchemaVersion string `json:"schemaVersion"`
			Programs      []struct {
				Content string `json:"content"`
			} `json:"programs"`
			TargetStores []string          `json:"targetStores"`
			Schemas      []json.RawMessage `json:"schemas"`
		}
		require.NoError(t, json.Unmarshal(value, &body))
		assert.Equal(t, "1.0.0", body.SchemaVersion)
		require.Len(t, body.Programs, 1)
		assert.Equal(t, "b3BhcXVlAHByb2dyYW0=", body.Programs[0].Content)
		assert.Equal(t, []string{"data-center-inventory"}, body.TargetStores)
		require.Len(t, body.Schemas, 1)
		assert.Contains(t, string(body.Schemas[0]), `"artifactRevision":"rev-schema"`)
		if first == nil {
			first = value
		} else {
			assert.Equal(t, first, value)
		}
	}
	require.NotNil(t, first)
}

func TestSourceMappingPackageMissingDependenciesAndConditionalResponses(t *testing.T) {
	t.Run("absent source schema and mapping have no partial package", func(t *testing.T) {
		server := artifactTestServer(mappingFixture())
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/missing/source-mappings/packages")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"schema-not-in-release"`)
		assert.NotContains(t, response.Body.String(), `"mapping":`)
		fixture := mappingFixture()
		fixture.mappings = map[string]model.SourceMappingDetail{}
		delete(fixture.selections, "source-mapping/device-mapping")
		response = serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.NotContains(t, response.Body.String(), `"mapping":`)
	})

	t.Run("resource failure returns no partial package", func(t *testing.T) {
		fixture := mappingFixture()
		fixture.resourceErr = postgres.ErrInconsistentState
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), `"mapping":`)
	})
}

func TestSourceMappingPackageRejectsMissingStoresAndCorruptSchemas(t *testing.T) {
	t.Run("missing canonical target stores", func(t *testing.T) {
		fixture := mappingFixture()
		fixture.stores = map[string][]model.StorageEntry{}
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), `"targetStores"`)
	})
	t.Run("corrupt source schema bytes", func(t *testing.T) {
		fixture := mappingFixture()
		schema := fixture.selections["schema/device"]
		schema.Payload.Digest = fixtureBytes([]byte("other"), schema.Payload.MediaType).Digest
		fixture.selections["schema/device"], fixture.artifacts[schema.Revision] = schema, schema
		response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.NotContains(t, response.Body.String(), `"sourceSchema":`)
	})
}

func TestSourceMappingPackageRejectsCorruptProgram(t *testing.T) {
	fixture := mappingFixture()
	mapping := fixture.selections["source-mapping/device-mapping"]
	resource := fixture.resources[mapping.Revision][0]
	resource.Digest = fixtureBytes([]byte("other"), resource.MediaType).Digest
	fixture.resources[mapping.Revision][0] = resource
	response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
	assert.Equal(t, http.StatusInternalServerError, response.Code)
}

func TestSourceMappingPackageOpaqueProgramBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "one byte", body: []byte{0xff}},
		{name: "exact 16 MiB", body: bytes.Repeat([]byte{'p'}, 16<<20)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := mappingFixture()
			mapping := fixture.selections["source-mapping/device-mapping"]
			resource := fixture.resources[mapping.Revision][0]
			resource.ByteDescriptor = fixtureBytes(test.body, resource.MediaType)
			fixture.resources[mapping.Revision][0] = resource
			detail := fixture.mappings[mapping.Revision]
			detail.Transformations = append([]model.MappingTransformation(nil), detail.Transformations...)
			detail.Transformations[0].ProgramDigest = resource.Digest
			setMappingFixture(fixture, mapping, detail)

			response := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/releases/1.0.0/source-mappings/device-mapping/package")
			require.Equal(t, http.StatusOK, response.Code)
			var body struct {
				Programs []struct {
					Size    int64  `json:"size"`
					Digest  string `json:"digest"`
					Content string `json:"content"`
				} `json:"programs"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Len(t, body.Programs, 1)
			assert.Equal(t, int64(len(test.body)), body.Programs[0].Size)
			assert.Equal(t, resource.Digest, body.Programs[0].Digest)
			decoded, err := base64.StdEncoding.DecodeString(body.Programs[0].Content)
			require.NoError(t, err)
			if len(test.body) == 0 {
				assert.Empty(t, decoded)
			} else {
				assert.Equal(t, test.body, decoded)
			}
		})
	}
}

func TestGeneratedPackageSizeBoundary(t *testing.T) {
	type packageBody struct {
		Content string `json:"content"`
	}
	const limit = maxGeneratedJSONBytes
	base, err := json.Marshal(packageBody{})
	require.NoError(t, err)
	content := strings.Repeat("x", limit-len(base))
	require.Len(t, mustJSON(t, packageBody{Content: content}), limit)

	t.Run("exact limit succeeds", func(t *testing.T) {
		writer := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(writer)
		context.Request = httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/package", nil)
		(&Server{}).writeBoundedJSON(context, "1.0.0", "1.0.0", packageBody{Content: content})
		assert.Equal(t, http.StatusOK, writer.Code)
		assert.Len(t, writer.Body.Bytes(), limit)
	})

	t.Run("one byte over fails before success", func(t *testing.T) {
		writer := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(writer)
		context.Request = httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/schemas/device/package", nil)
		(&Server{}).writeBoundedJSON(context, "1.0.0", "1.0.0", packageBody{Content: content + "x"})
		assert.Equal(t, http.StatusInternalServerError, writer.Code)
		assert.NotContains(t, writer.Body.String(), `"content"`)
		assert.Contains(t, writer.Body.String(), `"code":"inconsistent-registry-state"`)
	})
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	bytes, err := json.Marshal(value)
	require.NoError(t, err)
	return bytes
}

func schemaFixture() *artifactTestSnapshot {
	fixture := artifactFixture()
	schema := fixture.selections["schema/device"]
	schema.Payload.MediaType = "application/schema+json"
	fixture.selections["schema/device"] = schema
	fixture.artifacts[schema.Revision] = schema
	fixture.collections["1.0.0"][0] = schema
	context := fixture.selections["context/device"]
	context.Payload.MediaType = `application/ld+json;profile="http://www.w3.org/ns/json-ld#context"`
	fixture.selections["context/device"] = context
	fixture.artifacts[context.Revision] = context
	fixture.collections["1.0.0"][1] = context
	return fixture
}

func mappingFixture() *artifactTestSnapshot {
	fixture := schemaFixture()
	mapping := fixture.selections["source-mapping/device-mapping"]
	program := []byte("opaque\x00program")
	fixture.resources[mapping.Revision] = []model.Resource{{ArtifactRevision: mapping.Revision, Name: "program.bin", Role: "program", ByteDescriptor: fixtureBytes(program, "application/vnd.example.program")}}
	detail := model.SourceMappingDetail{SchemaVersion: "1.0.0", MappingRevision: mapping.Revision, SourceArtifactType: "device", SourceRevision: "rev-schema", TargetSchemaVersion: "1.0.0", EngineLanguage: "arbitrary-engine", EngineLanguageVersion: "9.8.7", ExecutionProfile: "arbitrary-profile", Transformations: []model.MappingTransformation{{TargetArtifactType: "device", TargetRevision: "rev-schema", Cardinality: "one", ProgramBinding: model.ProgramBinding{ProgramName: "program.bin", ProgramDigest: fixture.resources[mapping.Revision][0].Digest}}}}
	setMappingFixture(fixture, mapping, detail)
	return fixture
}

func setMappingFixture(fixture *artifactTestSnapshot, item model.Artifact, detail model.SourceMappingDetail) {
	programs := map[string]any{"transformations": make([]any, 0, len(detail.Transformations))}
	if detail.Condition != nil {
		programs["condition"] = map[string]any{"programRef": detail.Condition.ProgramName, "programDigest": detail.Condition.ProgramDigest}
	}
	dependencies := []model.Dependency{{ArtifactRevision: item.Revision, Kind: "schema", ArtifactType: detail.SourceArtifactType, Revision: detail.SourceRevision}}
	seen := map[string]bool{detail.SourceRevision: true}
	for _, transformation := range detail.Transformations {
		programs["transformations"] = append(programs["transformations"].([]any), map[string]any{"programRef": transformation.ProgramName, "programDigest": transformation.ProgramDigest, "cardinality": transformation.Cardinality, "targetSchema": map[string]any{"id": transformation.TargetArtifactType, "revision": transformation.TargetRevision}})
		if !seen[transformation.TargetRevision] {
			dependencies = append(dependencies, model.Dependency{ArtifactRevision: item.Revision, Kind: "schema", ArtifactType: transformation.TargetArtifactType, Revision: transformation.TargetRevision})
			seen[transformation.TargetRevision] = true
		}
	}
	data, err := json.Marshal(map[string]any{"$schema": "https://example.test/mapping-1.0.0", "formatVersion": "1.0.0", "id": item.Type, "sourceSchema": map[string]any{"id": detail.SourceArtifactType, "revision": detail.SourceRevision}, "engine": map[string]any{"language": detail.EngineLanguage, "languageVersion": detail.EngineLanguageVersion, "executionProfile": detail.ExecutionProfile}, "programs": programs})
	if err != nil {
		panic(err)
	}
	item.Payload = fixtureBytes(data, "application/vnd.apeiro.ocg.source-mapping.v1+json")
	fixture.artifacts[item.Revision], fixture.selections["source-mapping/"+item.Type] = item, item
	for index := range fixture.collections[detail.SchemaVersion] {
		if fixture.collections[detail.SchemaVersion][index].Revision == item.Revision {
			fixture.collections[detail.SchemaVersion][index] = item
		}
	}
	for index := range fixture.resources[item.Revision] {
		fixture.resources[item.Revision][index].ArtifactRevision = item.Revision
	}
	fixture.mappings[item.Revision], fixture.dependencies[item.Revision] = detail, dependencies
}
