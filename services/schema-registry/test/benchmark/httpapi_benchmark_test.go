package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

func BenchmarkEncodeGzipHead(b *testing.B) {
	body, err := json.Marshal(map[string]string{"schemaVersion": "1.0.0", "description": strings.Repeat("<&> 世界", 64*1024)})
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response := httptest.NewRecorder()
		err := httpapi.WriteRepresentation(response, httpapi.Representation{
			Status: http.StatusOK, MediaType: "application/json", Body: body,
			Method: http.MethodHead, AcceptCoding: "gzip",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeIdentityExact16MiB(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 16*1024*1024)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		encoded, err := httpapi.EncodeRepresentation(body, httpapi.Identity)
		if err != nil {
			b.Fatal(err)
		}
		_ = encoded
	}
}

func BenchmarkPackageBase64Expansion(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 16*1024*1024)
	destination := make([]byte, base64.StdEncoding.EncodedLen(len(payload)))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		base64.StdEncoding.Encode(destination, payload)
	}
}

func BenchmarkHTTPHealthEndpoint(b *testing.B) {
	server := httpapi.New(introspection.Config{Admission: introspection.AdmissionConfig{MaxInFlight: 1}}, nil)
	request := httptest.NewRequest(http.MethodHead, "/healthz", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatal(response.Code)
		}
	}
}

// BenchmarkMappingPackages measures complete multi-target packages and pages with shared or distinct program resources.
func BenchmarkMappingPackages(b *testing.B) {
	const mappingBase = "/v1/releases/1.0.0/source-mappings/device-mapping"
	const candidateBase = "/v1/releases/1.0.0/schemas/incoming/source-mappings"
	for _, programCount := range []int{1, 8} {
		b.Run(fmt.Sprintf("programs-%d", programCount), func(b *testing.B) {
			fixture := newMappingBenchmarkSnapshot(b, programCount)
			server := httpapi.NewWithServices(
				introspection.Config{Admission: introspection.AdmissionConfig{MaxInFlight: 1}},
				nil, release.New(fixture), artifact.New(fixture),
			)
			for _, path := range []string{mappingBase + "/package", candidateBase + "/packages?limit=1", candidateBase + "/packages"} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					b.Run(method+path, func(b *testing.B) {
						b.ReportAllocs()
						for b.Loop() {
							request := httptest.NewRequest(method, path, nil)
							request.Header.Set("Accept-Encoding", "gzip")
							response := httptest.NewRecorder()
							server.Handler().ServeHTTP(response, request)
							require.Equal(b, http.StatusOK, response.Code)
						}
					})
				}
			}
		})
	}
}

// mappingBenchmarkSnapshot supplies immutable in-memory reads, keeping database work outside the HTTP benchmark.
// Unused snapshot methods remain unimplemented so unexpected calls fail loudly.
type mappingBenchmarkSnapshot struct {
	release.Snapshot
	schemas  []model.Artifact
	mappings []model.SourceMapping
}

// newMappingBenchmarkSnapshot creates two candidates with optional conditions, repeated targets, and exact opaque program bytes.
func newMappingBenchmarkSnapshot(b *testing.B, programCount int) *mappingBenchmarkSnapshot {
	b.Helper()
	const version = "1.0.0"
	snapshot := &mappingBenchmarkSnapshot{}
	for _, schema := range []struct{ name, class, document string }{
		{"incoming", "source-record", "true"},
		{"device", "entity-type", `{"type":"object","title":"<&> 世界"}`},
		{"located-in", "edge-type", "false"},
	} {
		snapshot.schemas = append(snapshot.schemas, model.Artifact{
			Kind: "schema", Type: schema.name, Revision: "rev-" + schema.name,
			CreatedUnderSchemaVersion: version, SchemaClass: new(schema.class),
			CanonicalURI: "https://example.test/v1/resources/schema/" + schema.name + "/rev-" + schema.name,
			Payload:      benchmarkBytes([]byte(schema.document), "application/schema+json"),
		})
	}
	for candidate, name := range []string{"device-mapping", "device-mapping-two"} {
		revision := "rev-" + name
		program := model.Resource{
			ArtifactRevision: revision, Name: "program.bin", Role: "program",
			ByteDescriptor: benchmarkBytes([]byte{0, 0xff}, "application/octet-stream"),
		}
		mapping := model.SourceMapping{
			Descriptor: model.Descriptor{Artifact: model.Artifact{
				Kind: "source-mapping", Type: name, Revision: revision,
				CreatedUnderSchemaVersion: version, TargetSchemaVersion: new(version),
				CanonicalURI: "https://example.test/v1/resources/source-mapping/" + name + "/" + revision,
			}},
			Detail: model.SourceMappingDetail{
				SchemaVersion: version, MappingRevision: revision,
				SourceArtifactType: "incoming", SourceRevision: "rev-incoming", TargetSchemaVersion: version,
				EngineLanguage: "opaque", EngineLanguageVersion: "1.0.0", ExecutionProfile: "opaque-profile",
			},
		}
		targets := []model.SourceMappingTransformation{
			{TargetArtifactType: "device", TargetRevision: "rev-device", Cardinality: "one"},
			{TargetArtifactType: "located-in", TargetRevision: "rev-located-in", Cardinality: "many"},
			{TargetArtifactType: "device", TargetRevision: "rev-device", Cardinality: "one"},
		}
		if candidate == 0 {
			condition := model.Resource{
				ArtifactRevision: revision, Name: "a-condition.bin", Role: "program",
				ByteDescriptor: benchmarkBytes(nil, "application/octet-stream"),
			}
			mapping.Descriptor.Resources = append(mapping.Descriptor.Resources, condition)
			mapping.Detail.Condition = &model.ProgramBinding{ProgramName: condition.Name, ProgramDigest: condition.Digest}
			mapping.Descriptor.Artifact.Extensions = []byte(`{"org.example.note":"<&> 世界"}`)
			// These lengths exercise Base64 padding without defining a response-size threshold.
			for index := range programCount {
				resource := model.Resource{
					ArtifactRevision: revision, Name: fmt.Sprintf("program-%d.bin", index), Role: "program",
					ByteDescriptor: benchmarkBytes(bytes.Repeat([]byte{0xfb}, 64*1024+index), "application/octet-stream"),
				}
				mapping.Descriptor.Resources = append(mapping.Descriptor.Resources, resource)
				for _, target := range targets {
					target.ProgramBinding = model.ProgramBinding{ProgramName: resource.Name, ProgramDigest: resource.Digest}
					mapping.Detail.Transformations = append(mapping.Detail.Transformations, target)
				}
			}
		}
		mapping.Descriptor.Resources = append(mapping.Descriptor.Resources, program)
		target := targets[0]
		target.ProgramBinding = model.ProgramBinding{ProgramName: program.Name, ProgramDigest: program.Digest}
		mapping.Detail.Transformations = append(mapping.Detail.Transformations, target)
		programs := map[string]any{"transformations": []any{}}
		if condition := mapping.Detail.Condition; condition != nil {
			programs["condition"] = map[string]any{"programRef": condition.ProgramName, "programDigest": condition.ProgramDigest}
		}
		mapping.Descriptor.Dependencies = []model.Dependency{{ArtifactRevision: revision, Kind: "schema", ArtifactType: "incoming", Revision: "rev-incoming"}}
		seen := map[string]bool{"incoming": true}
		for _, transformation := range mapping.Detail.Transformations {
			programs["transformations"] = append(programs["transformations"].([]any), map[string]any{
				"programRef": transformation.ProgramName, "programDigest": transformation.ProgramDigest,
				"cardinality":  transformation.Cardinality,
				"targetSchema": map[string]any{"id": transformation.TargetArtifactType, "revision": transformation.TargetRevision},
			})
			if !seen[transformation.TargetArtifactType] {
				mapping.Descriptor.Dependencies = append(mapping.Descriptor.Dependencies, model.Dependency{
					ArtifactRevision: revision, Kind: "schema", ArtifactType: transformation.TargetArtifactType, Revision: transformation.TargetRevision,
				})
				seen[transformation.TargetArtifactType] = true
			}
		}
		payload, err := json.Marshal(map[string]any{
			"$schema": "https://example.test/mapping", "formatVersion": sourcemapping.FormatVersion, "id": name,
			"sourceSchema": map[string]any{"id": "incoming", "revision": "rev-incoming"},
			"engine":       map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque-profile"},
			"programs":     programs,
		})
		require.NoError(b, err)
		mapping.Descriptor.Artifact.Payload = benchmarkBytes(payload, sourcemapping.DescriptorMediaType)
		snapshot.mappings = append(snapshot.mappings, mapping)
	}
	return snapshot
}

// benchmarkBytes supplies exact digest and length metadata for benchmark content.
func benchmarkBytes(data []byte, mediaType string) model.ByteDescriptor {
	return model.ByteDescriptor{Bytes: data, Size: int64(len(data)), Digest: fmt.Sprintf("sha-256:%x", sha256.Sum256(data)), MediaType: mediaType}
}

// Read runs a benchmark request against the same immutable snapshot without database access.
func (s *mappingBenchmarkSnapshot) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	return fn(ctx, s)
}

// CurrentState supplies one current release and activation ceiling.
func (*mappingBenchmarkSnapshot) CurrentState() model.CurrentState {
	return model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}
}

// CurrentRelease resolves the fixed current release.
func (s *mappingBenchmarkSnapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	return s.Release(ctx, s.CurrentState().SchemaVersion)
}

// Release limits visibility to the benchmark's selected release.
func (*mappingBenchmarkSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	if version != "1.0.0" {
		return model.Release{}, postgres.ErrNotFound
	}
	return model.Release{SchemaVersion: version, ActivationSequence: 1}, nil
}

// ReleaseArtifact resolves schema or mapping selections needed for package assembly.
func (s *mappingBenchmarkSnapshot) ReleaseArtifact(_ context.Context, _ string, kind, name string) (model.Artifact, error) {
	for _, schema := range s.schemas {
		if schema.Kind == kind && schema.Type == name {
			return schema, nil
		}
	}
	for _, mapping := range s.mappings {
		if mapping.Descriptor.Artifact.Kind == kind && mapping.Descriptor.Artifact.Type == name {
			return mapping.Descriptor.Artifact, nil
		}
	}
	return model.Artifact{}, postgres.ErrNotFound
}

// ReleaseArtifacts returns ordered candidates with the requested source filter and keyset limit.
func (s *mappingBenchmarkSnapshot) ReleaseArtifacts(_ context.Context, _ string, filter model.ArtifactFilter, _, afterType *string, limit int) ([]model.Artifact, error) {
	items := make([]model.Artifact, 0, len(s.mappings))
	for _, mapping := range s.mappings {
		item := mapping.Descriptor.Artifact
		if filter.SourceArtifactType != "" && filter.SourceArtifactType != mapping.Detail.SourceArtifactType || afterType != nil && item.Type <= *afterType {
			continue
		}
		items = append(items, item)
	}
	return items[:min(len(items), limit)], nil
}

// Resources copies resource metadata so service normalization cannot mutate benchmark inputs.
func (s *mappingBenchmarkSnapshot) Resources(_ context.Context, revision string) ([]model.Resource, error) {
	for _, mapping := range s.mappings {
		if mapping.Detail.MappingRevision == revision {
			return append([]model.Resource(nil), mapping.Descriptor.Resources...), nil
		}
	}
	return nil, nil
}

// Resource retrieves exact program bytes from the owning mapping revision.
func (s *mappingBenchmarkSnapshot) Resource(ctx context.Context, revision, name string) (model.Resource, error) {
	resources, err := s.Resources(ctx, revision)
	if err != nil {
		return model.Resource{}, err
	}
	for _, resource := range resources {
		if resource.Name == name {
			return resource, nil
		}
	}
	return model.Resource{}, postgres.ErrNotFound
}

// Dependencies supplies exact source and target revisions for mapping validation.
func (s *mappingBenchmarkSnapshot) Dependencies(_ context.Context, revision string) ([]model.Dependency, error) {
	for _, mapping := range s.mappings {
		if mapping.Detail.MappingRevision == revision {
			return mapping.Descriptor.Dependencies, nil
		}
	}
	return nil, nil
}

// SourceMappingByRevision supplies the condition and ordered transformation projection.
func (s *mappingBenchmarkSnapshot) SourceMappingByRevision(_ context.Context, _, revision string) (model.SourceMappingDetail, error) {
	for _, mapping := range s.mappings {
		if mapping.Detail.MappingRevision == revision {
			return mapping.Detail, nil
		}
	}
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// StorageEntries supplies overlapping target placements for sorted store-union assembly.
func (*mappingBenchmarkSnapshot) StorageEntries(_ context.Context, version, name string) ([]model.StorageEntry, error) {
	stores := []model.StorageEntry{{SchemaVersion: version, ArtifactType: name, Store: "data-center-inventory"}}
	if name == "located-in" {
		stores = append(stores, model.StorageEntry{SchemaVersion: version, ArtifactType: name, Store: "insights-repository"})
	}
	return stores, nil
}
