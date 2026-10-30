// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	usecase "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/stretchr/testify/require"
)

const mappingBase = "/v1/releases/1.0.0/source-mappings/device-mapping"
const candidateBase = "/v1/releases/1.0.0/schemas/incoming/source-mappings"

func multiMappingFixture() *artifactTestSnapshot {
	fixture := mappingFixture()
	source := fixtureArtifact("rev-source", "schema", "incoming", stringPointer("source-record"), nil, []byte("true"))
	source.Payload.MediaType = "application/schema+json"
	edge := fixtureArtifact("rev-edge", "schema", "located-in", stringPointer("edge-type"), nil, []byte("false"))
	edge.Payload.MediaType = "application/schema+json"
	for _, schema := range []model.Artifact{source, edge} {
		fixture.artifacts[schema.Revision], fixture.selections["schema/"+schema.Type] = schema, schema
	}
	fixture.stores["1.0.0\x00located-in"] = []model.StorageEntry{{SchemaVersion: "1.0.0", ArtifactType: "located-in", Store: "insights-repository"}, {SchemaVersion: "1.0.0", ArtifactType: "located-in", Store: "data-center-inventory"}}
	mapping := fixture.selections["source-mapping/device-mapping"]
	detail := fixture.mappings[mapping.Revision]
	detail.SourceArtifactType, detail.SourceRevision = "incoming", "rev-source"
	condition := model.Resource{ArtifactRevision: mapping.Revision, Name: "a-condition.bin", Role: "program", ByteDescriptor: fixtureBytes(nil, "application/octet-stream")}
	fixture.resources[mapping.Revision] = append([]model.Resource{condition}, fixture.resources[mapping.Revision]...)
	detail.Condition = &model.ProgramBinding{ProgramName: condition.Name, ProgramDigest: condition.Digest}
	first := detail.Transformations[0]
	edgeTransformation := first
	edgeTransformation.TargetArtifactType, edgeTransformation.TargetRevision, edgeTransformation.Cardinality = "located-in", "rev-edge", "many"
	detail.Transformations = []model.MappingTransformation{first, edgeTransformation, first}
	setMappingFixture(fixture, mapping, detail)
	second := mapping
	second.Type, second.Revision = "device-mapping-two", "rev-mapping-two"
	secondDetail := detail
	secondDetail.MappingRevision, secondDetail.Condition = second.Revision, nil
	secondDetail.Transformations = []model.MappingTransformation{first}
	fixture.resources[second.Revision] = append([]model.Resource(nil), fixture.resources[mapping.Revision][1:]...)
	fixture.collections["1.0.0"] = append(fixture.collections["1.0.0"], second)
	setMappingFixture(fixture, second, secondDetail)
	return fixture
}

func mappingJSON(t testing.TB, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	return result
}

func TestMappingModelsAreCompleteAndEqualAcrossResources(t *testing.T) {
	server := artifactTestServer(multiMappingFixture())
	direct := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, mappingBase))
	for _, name := range []string{"schemaVersion", "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "targetSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "links", "sourceSchema", "engine", "programs"} {
		require.Contains(t, direct, name)
	}
	for _, absent := range []string{"metadata", "targetSchema", "program", "cardinality"} {
		require.NotContains(t, direct, absent)
	}
	for _, path := range []string{"/v1/releases/1.0.0/source-mappings?sourceArtifactType=incoming", candidateBase + "?sourceMappingArtifactType=device-mapping"} {
		collection := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, path))
		items := collection["items"].([]any)
		require.NotEmpty(t, items)
		require.Equal(t, direct, items[0])
	}
	programs := direct["programs"].(map[string]any)
	condition := programs["condition"].(map[string]any)
	require.Len(t, condition, 6)
	transformations := programs["transformations"].([]any)
	require.Len(t, transformations, 3)
	for _, value := range transformations {
		entry := value.(map[string]any)
		for _, name := range []string{"name", "role", "mediaType", "digest", "size", "href", "cardinality", "targetSchema"} {
			require.Contains(t, entry, name)
		}
		require.Len(t, entry["targetSchema"].(map[string]any), 3)
	}
	packageObject := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, mappingBase+"/package"))
	collection := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, candidateBase+"/packages?sourceMappingArtifactType=device-mapping"))
	require.Equal(t, packageObject, collection["items"].([]any)[0])
	require.Equal(t, direct, packageObject["mapping"])
	require.Equal(t, []any{"data-center-inventory", "insights-repository"}, packageObject["targetStores"])
	schemas := packageObject["schemas"].([]any)
	require.Len(t, schemas, 3)
	for index, expected := range []string{"incoming", "device", "located-in"} {
		metadata := schemas[index].(map[string]any)["metadata"].(map[string]any)
		require.Equal(t, expected, metadata["artifactType"])
		for _, name := range []string{"createdUnderSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "links"} {
			require.Contains(t, metadata, name)
		}
	}
	require.Equal(t, false, schemas[2].(map[string]any)["document"])
	bytePrograms := packageObject["programs"].([]any)
	require.Len(t, bytePrograms, 2, "shared transformation bytes must occur once")
	for _, value := range bytePrograms {
		program := value.(map[string]any)
		decoded, err := base64.StdEncoding.DecodeString(program["content"].(string))
		require.NoError(t, err)
		require.EqualValues(t, len(decoded), program["size"])
		require.Equal(t, fixtureBytes(decoded, "application/octet-stream").Digest, program["digest"])
		require.Equal(t, base64.StdEncoding.EncodeToString(decoded), program["content"])
	}
	generic := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/source-mapping/device-mapping"))
	require.Equal(t, mappingBase, generic["links"].(map[string]any)["self"])
	require.Equal(t, mappingBase+"/package", generic["links"].(map[string]any)["package"])
	metadata := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/schemas/incoming"))
	require.Equal(t, "/v1/releases/1.0.0/source-mappings?sourceArtifactType=incoming", metadata["links"].(map[string]any)["sourceMappings"])
}

func TestCandidateCollectionFiltersAndCursorBindings(t *testing.T) {
	server := artifactTestServer(multiMappingFixture())
	for _, base := range []string{candidateBase, candidateBase + "/packages"} {
		for _, filter := range []string{"missing", "device-mapping-two"} {
			body := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, base+"?sourceMappingArtifactType="+filter))
			require.Len(t, body, 4)
			if filter == "missing" {
				require.Empty(t, body["items"])
			} else {
				require.Len(t, body["items"], 1)
			}
			require.Nil(t, body["nextCursor"])
		}
		for _, query := range []string{"sourceMappingArtifactType=", "sourceMappingArtifactType=Upper", "sourceMappingArtifactType=" + strings.Repeat("x", 256), "sourceMappingArtifactType=a&sourceMappingArtifactType=b", "unknown=x", "limit=0", "limit=1001", "limit=", "cursor="} {
			response := serveArtifactRequest(server, http.MethodGet, base+"?"+query)
			require.Equal(t, http.StatusBadRequest, response.Code, query)
			require.Contains(t, response.Body.String(), `"code":"invalid-query-parameter"`)
		}
		first := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, base+"?limit=1"))
		require.Len(t, first["items"], 1)
		token := first["nextCursor"].(string)
		second := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, base+"?limit=1&cursor="+url.QueryEscape(token)))
		require.Len(t, second["items"], 1)
		require.Nil(t, second["nextCursor"])
		other := candidateBase
		if base == candidateBase {
			other += "/packages"
		}
		for _, path := range []string{other + "?limit=1", base + "?limit=2", base + "?limit=1&sourceMappingArtifactType=device-mapping", strings.Replace(base, "incoming", "device", 1) + "?limit=1"} {
			response := serveArtifactRequest(server, http.MethodGet, path+"&cursor="+url.QueryEscape(token))
			require.Equal(t, http.StatusBadRequest, response.Code, path)
			require.Contains(t, response.Body.String(), `"code":"invalid-cursor"`)
		}
		body := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, base+"?limit=1000"))
		require.Len(t, body["items"], 2)
		unrelated := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, strings.Replace(base, "incoming", "device", 1)+"?sourceMappingArtifactType=device-mapping"))
		require.Empty(t, unrelated["items"])
	}
	for _, path := range []string{mappingBase + "?limit=1", mappingBase + "/package?cursor=x", "/v1/releases/1.0.0/source-mappings?sourceMappingArtifactType=device-mapping"} {
		require.Equal(t, http.StatusBadRequest, serveArtifactRequest(server, http.MethodGet, path).Code)
	}
	for _, path := range []string{"/v1/releases/1.0.0/schemas/incoming/source-mapping", "/v1/releases/1.0.0/schemas/incoming/source-mapping/package"} {
		require.Equal(t, http.StatusNotFound, serveArtifactRequest(server, http.MethodGet, path).Code)
	}
}

func TestPackagePageLimitIncludesEveryReturnedPackage(t *testing.T) {
	fixture := multiMappingFixture()
	data := bytes.Repeat([]byte{0xff}, 16<<20)
	for _, revision := range []string{"rev-mapping", "rev-mapping-two"} {
		detail := fixture.mappings[revision]
		detail.Transformations = append([]model.MappingTransformation(nil), detail.Transformations...)
		for index := range fixture.resources[revision] {
			resource := &fixture.resources[revision][index]
			resource.ByteDescriptor = fixtureBytes(data, resource.MediaType)
		}
		for index := range detail.Transformations {
			detail.Transformations[index].ProgramDigest = fixtureBytes(data, "application/octet-stream").Digest
		}
		if detail.Condition != nil {
			condition := *detail.Condition
			condition.ProgramDigest = fixtureBytes(data, "application/octet-stream").Digest
			detail.Condition = &condition
		}
		setMappingFixture(fixture, fixture.artifacts[revision], detail)
	}
	server := artifactTestServer(fixture)
	for _, path := range []string{mappingBase + "/package", mappingBase + "-two/package", candidateBase + "/packages?limit=1"} {
		response := serveArtifactRequest(server, http.MethodHead, path)
		require.Equal(t, http.StatusOK, response.Code, path)
	}
	response := serveArtifactRequest(server, http.MethodGet, candidateBase+"/packages")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	require.NotContains(t, response.Body.String(), `"items"`)
}

func BenchmarkMultiTargetPackagePage(b *testing.B) {
	server := artifactTestServer(multiMappingFixture())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		request := httptest.NewRequest(http.MethodHead, candidateBase+"/packages", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatal(response.Code)
		}
	}
}

func TestPackagePageFailsAtomicallyForLaterMapping(t *testing.T) {
	for _, change := range []func(*artifactTestSnapshot){
		func(f *artifactTestSnapshot) {
			value := f.mappings["rev-mapping-two"]
			value.Transformations = append([]model.MappingTransformation(nil), value.Transformations...)
			value.Transformations[0].ProgramDigest = fixtureBytes(nil, "application/octet-stream").Digest
			f.mappings["rev-mapping-two"] = value
		},
		func(f *artifactTestSnapshot) { f.resources["rev-mapping-two"][0].Bytes = []byte("corrupt") },
	} {
		fixture := multiMappingFixture()
		change(fixture)
		server := artifactTestServer(fixture)
		require.Equal(t, http.StatusOK, serveArtifactRequest(server, http.MethodGet, mappingBase+"/package").Code)
		response := serveArtifactRequest(server, http.MethodGet, candidateBase+"/packages")
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
		require.NotContains(t, response.Body.String(), `"items"`)
		require.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	}
}

type alternatingMappingReader struct {
	snapshot *artifactTestSnapshot
	calls    atomic.Int64
}

func (r *alternatingMappingReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	copy := *r.snapshot
	if r.calls.Add(1)%2 == 0 {
		copy.current.SchemaVersion = "2.0.0"
	}
	return fn(ctx, &copy)
}

func TestMappingRequestsUseOneSnapshotAndConditionalRepresentation(t *testing.T) {
	fixture := multiMappingFixture()
	fixture.releases["2.0.0"] = model.Release{SchemaVersion: "2.0.0"}
	reader := &alternatingMappingReader{snapshot: fixture}
	server := artifactTestServer(fixture)
	server.artifacts, server.releases = usecase.New(reader), release.New(reader)
	paths := []string{mappingBase, mappingBase + "/package", candidateBase, candidateBase + "/packages"}
	for _, path := range paths {
		before := reader.calls.Load()
		response := serveArtifactRequest(server, http.MethodGet, path)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, before+1, reader.calls.Load(), "one read boundary per request")
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("If-None-Match", response.Header().Get("ETag"))
		conditional := httptest.NewRecorder()
		server.Handler().ServeHTTP(conditional, request)
		require.Equal(t, http.StatusNotModified, conditional.Code)
		require.Empty(t, conditional.Body.String())
		require.NotEqual(t, response.Header().Get("Schema-Version"), conditional.Header().Get("Schema-Version"))
		require.Equal(t, "1.0.0", conditional.Header().Get("Selected-Schema-Version"))
		request = httptest.NewRequest(http.MethodHead, path, nil)
		request.Header.Set("Accept-Encoding", "gzip")
		head := httptest.NewRecorder()
		server.Handler().ServeHTTP(head, request)
		require.Equal(t, http.StatusOK, head.Code)
		require.Empty(t, head.Body.String())
		require.Equal(t, "gzip", head.Header().Get("Content-Encoding"))
		require.NotEqual(t, response.Header().Get("ETag"), head.Header().Get("ETag"))
	}
	var group sync.WaitGroup
	results := make(chan int, 32)
	for index := range 32 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results <- serveArtifactRequest(server, http.MethodGet, paths[index%len(paths)]).Code
		}(index)
	}
	group.Wait()
	close(results)
	for status := range results {
		require.Equal(t, http.StatusOK, status)
	}
}
