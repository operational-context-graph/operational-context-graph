// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	usecase "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	"github.com/stretchr/testify/require"
)

type mappingRegressionReader struct{ snapshot release.Snapshot }

func (r mappingRegressionReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	return fn(ctx, r.snapshot)
}

func mappingRegressionServer(snapshot release.Snapshot) *Server {
	server := artifactTestServer(mappingFixture())
	reader := mappingRegressionReader{snapshot: snapshot}
	server.artifacts, server.releases = usecase.New(reader), release.New(reader)
	return server
}

type schemaLookupSnapshot struct {
	release.Snapshot
	lookups map[string]int
}

func (s *schemaLookupSnapshot) ReleaseArtifactMetadata(ctx context.Context, version, kind, artifactType string) (model.Artifact, error) {
	s.lookups[artifactType]++
	return s.ReleaseArtifact(ctx, version, kind, artifactType)
}

func repeatedTargetFixture() *artifactTestSnapshot {
	fixture := multiMappingFixture()
	mapping := fixture.artifacts["rev-mapping"]
	detail := fixture.mappings[mapping.Revision]
	original := detail.Transformations
	detail.Transformations = make([]model.MappingTransformation, strictjson.MaxArrayEntries)
	for index := range detail.Transformations {
		detail.Transformations[index] = original[index%2]
	}
	setMappingFixture(fixture, mapping, detail)
	return fixture
}

func TestMappingSchemaLookupsScaleWithDistinctSchemas(t *testing.T) {
	fixture := repeatedTargetFixture()
	snapshot := &schemaLookupSnapshot{Snapshot: fixture}
	server := mappingRegressionServer(snapshot)
	for _, path := range []string{mappingBase, mappingBase + "/package", "/v1/releases/1.0.0/source-mappings", candidateBase, candidateBase + "/packages"} {
		t.Run(path, func(t *testing.T) {
			for range 2 {
				snapshot.lookups = make(map[string]int)
				response := serveArtifactRequest(server, http.MethodGet, path)
				require.Equal(t, http.StatusOK, response.Code)
				require.Equal(t, map[string]int{"incoming": 1, "device": 1, "located-in": 1}, snapshot.lookups)
			}
		})
	}
}

type fallbackSchemaLookupSnapshot struct {
	release.Snapshot
	lookups map[string]int
}

func (s *fallbackSchemaLookupSnapshot) ReleaseArtifact(ctx context.Context, version, kind, artifactType string) (model.Artifact, error) {
	if kind == "schema" {
		s.lookups[artifactType]++
	}
	return s.Snapshot.ReleaseArtifact(ctx, version, kind, artifactType)
}

func TestMappingSchemaLookupCachePreservesFallbackAndValidation(t *testing.T) {
	fixture := repeatedTargetFixture()
	snapshot := &fallbackSchemaLookupSnapshot{Snapshot: fixture, lookups: make(map[string]int)}
	server := mappingRegressionServer(snapshot)
	response := serveArtifactRequest(server, http.MethodGet, mappingBase)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, map[string]int{"incoming": 1, "device": 1, "located-in": 1}, snapshot.lookups)
	var body MappingResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Programs.Transformations, strictjson.MaxArrayEntries)
	require.Equal(t, "many", body.Programs.Transformations[1].Cardinality)
	require.Equal(t, "located-in", body.Programs.Transformations[1].TargetSchema.ArtifactType)

	// A late reference still validates its own revision against cached metadata.
	mapping := fixture.artifacts["rev-mapping"]
	detail := fixture.mappings[mapping.Revision]
	detail.Transformations[len(detail.Transformations)-1].TargetRevision = "wrong-revision"
	setMappingFixture(fixture, mapping, detail)
	snapshot.lookups = make(map[string]int)
	response = serveArtifactRequest(server, http.MethodGet, mappingBase)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	require.NotContains(t, response.Body.String(), `"programs"`)
	require.Equal(t, map[string]int{"incoming": 1, "device": 1, "located-in": 1}, snapshot.lookups)
}

func TestMappingCachedSourceStillValidatesTargetClassification(t *testing.T) {
	fixture := mappingFixture()
	schema := fixture.selections["schema/device"]
	schema.SchemaClass = stringPointer("source-record")
	fixture.selections["schema/device"] = schema
	snapshot := &schemaLookupSnapshot{Snapshot: fixture, lookups: make(map[string]int)}
	response := serveArtifactRequest(mappingRegressionServer(snapshot), http.MethodGet, mappingBase)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	require.Equal(t, map[string]int{"device": 1}, snapshot.lookups)
}

type programReadSnapshot struct {
	release.Snapshot
	reads map[string]int
}

func (s *programReadSnapshot) Resources(ctx context.Context, revision string) ([]model.Resource, error) {
	resources, err := s.Snapshot.Resources(ctx, revision)
	for index := range resources {
		resources[index].Bytes = nil
	}
	return resources, err
}

func (s *programReadSnapshot) Resource(ctx context.Context, revision, name string) (model.Resource, error) {
	s.reads[name]++
	resource, err := s.Snapshot.Resource(ctx, revision, name)
	if err == nil && resource.Role != "program" {
		return model.Resource{}, errors.New("ancillary bytes must not be loaded for a mapping package")
	}
	return resource, err
}

func TestMappingAncillaryResourcesStayMetadataOnly(t *testing.T) {
	fixture := multiMappingFixture()
	data := bytes.Repeat([]byte{0xff}, 16<<20)
	for _, name := range []string{"lookup-a.bin", "lookup-b.bin", "lookup-c.bin"} {
		fixture.resources["rev-mapping"] = append(fixture.resources["rev-mapping"], model.Resource{ArtifactRevision: "rev-mapping", Name: name, Role: "metadata", ByteDescriptor: fixtureBytes(data, "application/octet-stream")})
	}
	snapshot := &programReadSnapshot{Snapshot: fixture, reads: make(map[string]int)}
	server := mappingRegressionServer(snapshot)
	metadata := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, mappingBase))
	require.Len(t, metadata["resources"], 5)
	require.Empty(t, snapshot.reads)
	for _, path := range []string{mappingBase + "/package", candidateBase + "/packages?sourceMappingArtifactType=device-mapping"} {
		snapshot.reads = make(map[string]int)
		body := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, path))
		if strings.Contains(path, "/packages?") {
			body = body["items"].([]any)[0].(map[string]any)
		}
		require.Equal(t, metadata, body["mapping"])
		programs := body["programs"].([]any)
		require.Len(t, programs, 2)
		require.Equal(t, "a-condition.bin", programs[0].(map[string]any)["name"])
		require.Equal(t, "program.bin", programs[1].(map[string]any)["name"])
		require.Equal(t, map[string]int{"a-condition.bin": 1, "program.bin": 1}, snapshot.reads)
	}
	for _, path := range []string{"/v1/releases/1.0.0/source-mappings", candidateBase} {
		body := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, path))
		require.Equal(t, metadata, body["items"].([]any)[0])
	}
}

func TestMappingProgramBindingsStillRejectInvalidResources(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*artifactTestSnapshot)
	}{
		{"condition binds non-program", func(f *artifactTestSnapshot) { f.resources["rev-mapping"][0].Role = "metadata" }},
		{"transformation binds non-program", func(f *artifactTestSnapshot) { f.resources["rev-mapping"][1].Role = "metadata" }},
		{"unreferenced program", func(f *artifactTestSnapshot) {
			resource := f.resources["rev-mapping"][0]
			resource.Name = "extra.bin"
			f.resources["rev-mapping"] = append(f.resources["rev-mapping"], resource)
		}},
		{"invalid ancillary role", func(f *artifactTestSnapshot) {
			f.resources["rev-mapping"] = append(f.resources["rev-mapping"], model.Resource{ArtifactRevision: "rev-mapping", Name: "lookup.bin", Role: "Invalid", ByteDescriptor: fixtureBytes(nil, "application/octet-stream")})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := multiMappingFixture()
			test.mutate(fixture)
			server := artifactTestServer(fixture)
			for _, path := range []string{mappingBase, mappingBase + "/package"} {
				response := serveArtifactRequest(server, http.MethodGet, path)
				require.Equal(t, http.StatusInternalServerError, response.Code)
				require.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
			}
		})
	}
}

type selectedReleaseErrorSnapshot struct {
	release.Snapshot
	errors map[string]error
}

func (s selectedReleaseErrorSnapshot) Release(ctx context.Context, version string) (model.Release, error) {
	if err := s.errors[version]; err != nil {
		return model.Release{}, err
	}
	return s.Snapshot.Release(ctx, version)
}

func TestMappingRoutesDistinguishReleaseAndItemFailures(t *testing.T) {
	for _, test := range []struct {
		name, version string
		err           error
		status        int
		code          string
	}{
		{"unknown release", "9.0.0", postgres.ErrNotFound, http.StatusNotFound, "release-not-found"},
		{"inactive release", "1.1.0", postgres.ErrNotFound, http.StatusNotFound, "release-not-found"},
		{"database failure", "1.1.0", errors.New("read failed"), http.StatusServiceUnavailable, "registry-unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := multiMappingFixture()
			fixture.releases["1.1.0"] = model.Release{SchemaVersion: "1.1.0"}
			snapshot := selectedReleaseErrorSnapshot{Snapshot: fixture, errors: map[string]error{test.version: test.err}}
			server := mappingRegressionServer(snapshot)
			for _, path := range []string{mappingBase, mappingBase + "/package", "/v1/releases/1.0.0/source-mappings", candidateBase, candidateBase + "/packages"} {
				path = strings.Replace(path, "/1.0.0/", "/"+test.version+"/", 1)
				get := serveArtifactRequest(server, http.MethodGet, path)
				require.Equal(t, test.status, get.Code, path)
				require.Contains(t, get.Body.String(), `"code":"`+test.code+`"`, path)
				require.Equal(t, "1.0.0", get.Header().Get("Schema-Version"))
				require.Empty(t, get.Header().Get("Selected-Schema-Version"))
				head := serveArtifactRequest(server, http.MethodHead, path)
				require.Equal(t, get.Code, head.Code)
				require.Empty(t, head.Body.String())
				require.Equal(t, get.Header().Get("Schema-Version"), head.Header().Get("Schema-Version"))
			}
		})
	}
	server := artifactTestServer(multiMappingFixture())
	for _, test := range []struct{ path, code string }{
		{mappingBase + "-missing", "artifact-not-in-release"},
		{mappingBase + "-missing/package", "artifact-not-in-release"},
		{strings.Replace(candidateBase, "incoming", "missing", 1), "schema-not-in-release"},
		{strings.Replace(candidateBase, "incoming", "missing", 1) + "/packages", "schema-not-in-release"},
	} {
		response := serveArtifactRequest(server, http.MethodGet, test.path)
		require.Equal(t, http.StatusNotFound, response.Code)
		require.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
		require.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	}
}
