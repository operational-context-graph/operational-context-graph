package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/limits"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// serviceSnapshot supplies controlled artifacts and dependencies and records read and resource-load counts.
// Unimplemented snapshot methods panic, exposing unexpected calls rather than returning misleading zero values.
type serviceSnapshot struct {
	release.Snapshot
	current        model.CurrentState
	artifacts      map[string]model.Artifact
	resources      map[string][]model.Resource
	dependencies   map[string][]model.Dependency
	bindings       []model.SchemaContextBinding
	stores         map[string][]model.StorageEntry
	detail         model.SourceMappingDetail
	failures       map[string]error
	loadedResource *model.Resource
	reads          int
	resourceReads  int
	history        []model.ReleaseArtifact
	historyMaximum int64
}

// Read counts snapshot invocations and injects optional boundary failures before callback execution.
func (s *serviceSnapshot) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	s.reads++
	if err := s.failures["read"]; err != nil {
		return err
	}
	return fn(ctx, s)
}

// CurrentState returns the fixture's current pointer and activation ceiling.
func (s *serviceSnapshot) CurrentState() model.CurrentState { return s.current }

// CurrentRelease supplies the current version or a controlled current-release failure.
func (s *serviceSnapshot) CurrentRelease(context.Context) (model.Release, error) {
	return model.Release{SchemaVersion: s.current.SchemaVersion}, s.failures["current"]
}

// Release supplies selected-release identity or its injected failure without consulting a database.
func (s *serviceSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	return model.Release{SchemaVersion: version}, s.failures["release"]
}

// ReleaseArtifact finds a fixture selection by kind and logical type, with operation-specific failure injection.
func (s *serviceSnapshot) ReleaseArtifact(_ context.Context, _, kind, name string) (model.Artifact, error) {
	if err := s.failures["artifact/"+kind+"/"+name]; err != nil {
		return model.Artifact{}, err
	}
	for _, value := range s.artifacts {
		if value.Kind == kind && value.Type == name {
			return value, nil
		}
	}
	return model.Artifact{}, postgres.ErrNotFound
}

// Artifact resolves fixture revisions or injects lookup failures for dependency and immutable-artifact checks.
func (s *serviceSnapshot) Artifact(_ context.Context, revision string) (model.Artifact, error) {
	if err := s.failures["revision/"+revision]; err != nil {
		return model.Artifact{}, err
	}
	value, ok := s.artifacts[revision]
	if !ok {
		return model.Artifact{}, postgres.ErrNotFound
	}
	return value, nil
}

// Resources returns an independent metadata slice so service normalization cannot alter the fixture's resource list.
func (s *serviceSnapshot) Resources(_ context.Context, revision string) ([]model.Resource, error) {
	return append([]model.Resource(nil), s.resources[revision]...), s.failures["resources/"+revision]
}

// Dependencies supplies controlled exact-revision dependency evidence and optional lookup failures.
func (s *serviceSnapshot) Dependencies(_ context.Context, revision string) ([]model.Dependency, error) {
	return s.dependencies[revision], s.failures["dependencies/"+revision]
}

// ContextBindings supplies the fixture's context selections or an injected binding-query failure.
func (s *serviceSnapshot) ContextBindings(context.Context, string, string) ([]model.SchemaContextBinding, error) {
	return s.bindings, s.failures["bindings"]
}

// StorageEntries supplies copied logical placements or a schema-specific storage failure.
func (s *serviceSnapshot) StorageEntries(_ context.Context, _, name string) ([]model.StorageEntry, error) {
	return append([]model.StorageEntry(nil), s.stores[name]...), s.failures["stores/"+name]
}

// SourceMappingByRevision supplies mutable projection evidence for descriptor-agreement and dependency-failure cases.
func (s *serviceSnapshot) SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error) {
	return s.detail, s.failures["mapping"]
}

// Resource counts content loads and supports corrupt loaded-resource overrides independent of declared metadata.
func (s *serviceSnapshot) Resource(_ context.Context, revision, name string) (model.Resource, error) {
	s.resourceReads++
	if err := s.failures["resource"]; err != nil {
		return model.Resource{}, err
	}
	if s.loadedResource != nil {
		return *s.loadedResource, nil
	}
	for _, value := range s.resources[revision] {
		if value.Name == name {
			return value, nil
		}
	}
	return model.Resource{}, postgres.ErrNotFound
}

// ReleaseArtifacts supplies the fixture mapping collection or a controlled list-query failure.
func (s *serviceSnapshot) ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error) {
	return []model.Artifact{s.artifacts["mapping-r1"]}, s.failures["list"]
}

// ArtifactHistory records the requested activation ceiling and supplies configured logical history or its failure.
func (s *serviceSnapshot) ArtifactHistory(_ context.Context, _, _ string, _ *int64, maximum int64, _ int) ([]model.ReleaseArtifact, error) {
	s.historyMaximum = maximum
	return s.history, s.failures["history"]
}

// URIEntry supplies a release-scoped source-schema resolution or a controlled catalog failure.
func (s *serviceSnapshot) URIEntry(_ context.Context, version, uri string) (model.URIEntry, error) {
	return model.URIEntry{SchemaVersion: version, URI: uri, ArtifactRevision: "source-r1"}, s.failures["uri"]
}

// testBytes creates payload metadata whose declared digest and size match the fixture content.
func testBytes(raw []byte, mediaType string) model.ByteDescriptor {
	digest := sha256.Sum256(raw)
	return model.ByteDescriptor{Bytes: raw, Size: int64(len(raw)), Digest: "sha-256:" + hex.EncodeToString(digest[:]), MediaType: mediaType}
}

// newServiceSnapshot creates a consistent mapping closure with boolean schemas, opaque program bytes, and a canonical target store.
func newServiceSnapshot() *serviceSnapshot {
	sourceClass, targetClass, version := "source-record", "entity-type", "1.0.0"
	program := model.Resource{ArtifactRevision: "mapping-r1", Name: "program.bin", Role: "program", ByteDescriptor: testBytes([]byte("opaque\x00program"), "application/octet-stream")}
	mappingBytes := []byte(fmt.Sprintf(`{"$schema":"https://example.test/mapping","formatVersion":"1.0.0","id":"mapping","sourceSchema":{"id":"source","revision":"source-r1"},"engine":{"language":"opaque","languageVersion":"1.0.0","executionProfile":"test"},"programs":{"transformations":[{"programRef":"program.bin","programDigest":%q,"cardinality":"one","targetSchema":{"id":"target","revision":"target-r1"}}]}}`, program.Digest))
	return &serviceSnapshot{
		current: model.CurrentState{SchemaVersion: version, ActivationSequence: 1, SnapshotSequence: 1},
		artifacts: map[string]model.Artifact{
			"source-r1":  {Kind: "schema", Type: "source", Revision: "source-r1", SchemaClass: &sourceClass, Payload: testBytes([]byte("true"), "application/schema+json")},
			"target-r1":  {Kind: "schema", Type: "target", Revision: "target-r1", SchemaClass: &targetClass, Payload: testBytes([]byte("false"), "application/schema+json")},
			"mapping-r1": {Kind: "source-mapping", Type: "mapping", Revision: "mapping-r1", TargetSchemaVersion: &version, Payload: testBytes(mappingBytes, "application/vnd.apeiro.ocg.source-mapping.v1+json")},
		},
		resources: map[string][]model.Resource{"mapping-r1": {program}},
		dependencies: map[string][]model.Dependency{"mapping-r1": {
			{Kind: "schema", ArtifactType: "source", Revision: "source-r1"},
			{Kind: "schema", ArtifactType: "target", Revision: "target-r1"},
		}},
		stores:   map[string][]model.StorageEntry{"target": {{SchemaVersion: version, ArtifactType: "target", Store: "data-center-inventory"}}},
		detail:   model.SourceMappingDetail{SchemaVersion: version, MappingRevision: "mapping-r1", SourceArtifactType: "source", SourceRevision: "source-r1", TargetSchemaVersion: version, EngineLanguage: "opaque", EngineLanguageVersion: "1.0.0", ExecutionProfile: "test", Transformations: []model.SourceMappingTransformation{{ProgramBinding: model.ProgramBinding{ProgramName: program.Name, ProgramDigest: program.Digest}, TargetArtifactType: "target", TargetRevision: "target-r1", Cardinality: "one"}}},
		failures: make(map[string]error),
	}
}

// TestSourceMappingPackageUsesOneSnapshotAndExactBytes verifies one read boundary, exact programs, unique schemas, target stores, and unchanged stored bytes.
func TestSourceMappingPackageUsesOneSnapshotAndExactBytes(t *testing.T) {
	snapshot := newServiceSnapshot()
	result, err := New(snapshot).SourceMappingPackage(t.Context(), "1.0.0", "mapping")
	require.NoError(t, err)
	assert.Equal(t, 1, snapshot.reads)
	assert.Equal(t, 1, snapshot.resourceReads)
	assert.Equal(t, snapshot.current, result.Current)
	assert.Equal(t, "1.0.0", result.Selected)
	require.Len(t, result.Value.Programs, 1)
	assert.Equal(t, snapshot.resources["mapping-r1"][0], result.Value.Programs[0])
	require.Len(t, result.Value.Schemas, 2)
	assert.Equal(t, "source", result.Value.Schemas[0].Artifact.Type)
	assert.Equal(t, "target", result.Value.Schemas[1].Artifact.Type)
	assert.Equal(t, snapshot.stores["target"], result.Value.Stores)
	assert.Empty(t, result.Value.Mapping.Artifact.Payload.Bytes)
	assert.Empty(t, result.Value.Mapping.Resources[0].Bytes)
	assert.NotEmpty(t, snapshot.artifacts["mapping-r1"].Payload.Bytes, "metadata projection must not mutate stored bytes")
}

// TestSourceMappingPropagatesDependencyFailures verifies lookup causes retain resolved versions and missing required dependencies become inconsistent state.
func TestSourceMappingPropagatesDependencyFailures(t *testing.T) {
	for _, operation := range []string{
		"artifact/source-mapping/mapping",
		"resources/mapping-r1",
		"dependencies/mapping-r1",
		"mapping",
		"artifact/schema/source",
		"artifact/schema/target",
		"resource",
		"resources/source-r1",
		"dependencies/target-r1",
		"stores/target",
	} {
		t.Run(operation, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			cause := errors.New("controlled dependency failure")
			snapshot.failures[operation] = cause
			_, err := New(snapshot).SourceMappingPackage(t.Context(), "1.0.0", "mapping")
			require.ErrorIs(t, err, cause)
			current, selected, ok := release.RequestMetadata(err)
			require.True(t, ok)
			assert.Equal(t, snapshot.current, current)
			assert.Equal(t, "1.0.0", selected)
		})
	}
	for _, operation := range []string{"mapping", "artifact/schema/source", "resource"} {
		t.Run("missing/"+operation, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			snapshot.failures[operation] = postgres.ErrNotFound
			_, err := New(snapshot).SourceMappingPackage(t.Context(), "1.0.0", "mapping")
			require.ErrorIs(t, err, postgres.ErrInconsistentState)
			assert.NotErrorIs(t, err, postgres.ErrNotFound)
		})
	}
}

// TestSourceMappingRejectsDescriptorAndProjectionDisagreement verifies identity, engine, schema, store, program, and content mismatches fail as inconsistent state.
func TestSourceMappingRejectsDescriptorAndProjectionDisagreement(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*serviceSnapshot)
	}{
		{name: "mapping revision", mutate: func(s *serviceSnapshot) { s.detail.MappingRevision = "wrong" }},
		{name: "selected release", mutate: func(s *serviceSnapshot) { s.detail.SchemaVersion = "2.0.0" }},
		{name: "engine", mutate: func(s *serviceSnapshot) { s.detail.EngineLanguage = "wrong" }},
		{name: "transformations", mutate: func(s *serviceSnapshot) { s.detail.Transformations = nil }},
		{name: "source revision", mutate: func(s *serviceSnapshot) { s.detail.SourceRevision = "wrong" }},
		{name: "missing dependency", mutate: func(s *serviceSnapshot) { s.dependencies["mapping-r1"] = nil }},
		{name: "no target stores", mutate: func(s *serviceSnapshot) { s.stores["target"] = nil }},
		{name: "wrong store owner", mutate: func(s *serviceSnapshot) { s.stores["target"][0].ArtifactType = "wrong" }},
		{name: "wrong store release", mutate: func(s *serviceSnapshot) { s.stores["target"][0].SchemaVersion = "2.0.0" }},
		{name: "program metadata", mutate: func(s *serviceSnapshot) {
			value := s.resources["mapping-r1"][0]
			value.Role = "wrong"
			s.loadedResource = &value
		}},
		{name: "program bytes", mutate: func(s *serviceSnapshot) {
			value := s.resources["mapping-r1"][0]
			value.Bytes = []byte("corrupt")
			s.loadedResource = &value
		}},
		{name: "invalid source media", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["source-r1"]
			value.Payload.MediaType = "text/plain"
			s.artifacts["source-r1"] = value
		}},
		{name: "invalid target media", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["target-r1"]
			value.Payload.MediaType = "text/plain"
			s.artifacts["target-r1"] = value
		}},
		{name: "invalid source JSON", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["source-r1"]
			value.Payload = testBytes([]byte("{"), "application/schema+json")
			s.artifacts["source-r1"] = value
		}},
		{name: "invalid target JSON", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["target-r1"]
			value.Payload = testBytes([]byte("{"), "application/schema+json")
			s.artifacts["target-r1"] = value
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			test.mutate(snapshot)
			_, err := New(snapshot).SourceMappingPackage(t.Context(), "1.0.0", "mapping")
			require.ErrorIs(t, err, postgres.ErrInconsistentState)
		})
	}
}

// TestSchemaStoresRejectsInvalidAssignments verifies missing classification and source-record placements fail integrity checks while lookup failures retain their causes.
func TestSchemaStoresRejectsInvalidAssignments(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*serviceSnapshot)
	}{
		{name: "missing class", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["source-r1"]
			value.SchemaClass = nil
			s.artifacts["source-r1"] = value
		}},
		{name: "source record stores", mutate: func(s *serviceSnapshot) { s.stores["source"] = s.stores["target"] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			test.mutate(snapshot)
			_, err := New(snapshot).SchemaStores(t.Context(), "1.0.0", "source")
			require.ErrorIs(t, err, postgres.ErrInconsistentState)
		})
	}
	for _, operation := range []string{"artifact/schema/source", "stores/source"} {
		snapshot := newServiceSnapshot()
		cause := errors.New("store lookup failed")
		snapshot.failures[operation] = cause
		_, err := New(snapshot).SchemaStores(t.Context(), "1.0.0", "source")
		require.ErrorIs(t, err, cause)
	}
}

// TestSchemaPackageRejectsInvalidMediaAndLookupFailures verifies invalid schema/context media and relationship failures return no partial context package.
func TestSchemaPackageRejectsInvalidMediaAndLookupFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*serviceSnapshot)
		want   error
	}{
		{name: "schema media", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["source-r1"]
			value.Payload.MediaType = "text/plain"
			s.artifacts["source-r1"] = value
		}, want: postgres.ErrInconsistentState},
		{name: "context media", mutate: func(s *serviceSnapshot) {
			value := s.artifacts["context-r1"]
			value.Payload.MediaType = "application/json"
			s.artifacts["context-r1"] = value
		}, want: postgres.ErrInconsistentState},
		{name: "bindings", mutate: func(s *serviceSnapshot) { s.failures["bindings"] = context.DeadlineExceeded }, want: context.DeadlineExceeded},
		{name: "resources", mutate: func(s *serviceSnapshot) { s.failures["resources/context-r1"] = context.DeadlineExceeded }, want: context.DeadlineExceeded},
		{name: "dependencies", mutate: func(s *serviceSnapshot) { s.failures["dependencies/context-r1"] = context.DeadlineExceeded }, want: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			snapshot.artifacts["context-r1"] = model.Artifact{Kind: "context", Revision: "context-r1", Payload: testBytes([]byte(`{"@context":{}}`), `application/ld+json;profile="http://www.w3.org/ns/json-ld#context"`)}
			snapshot.bindings = []model.SchemaContextBinding{{ContextRevision: "context-r1"}}
			test.mutate(snapshot)
			result, err := New(snapshot).SchemaPackage(t.Context(), "1.0.0", "source")
			require.ErrorIs(t, err, test.want)
			assert.Empty(t, result.Value.Contexts)
		})
	}
}

// TestArtifactReadsPreserveFailureScope verifies current, selected-release, and artifact failures retain only versions resolved before the failure.
func TestArtifactReadsPreserveFailureScope(t *testing.T) {
	for _, test := range []struct {
		operation string
		selected  string
		want      error
	}{
		{operation: "current", want: context.DeadlineExceeded},
		{operation: "release", want: ErrReleaseNotFound},
		{operation: "artifact/schema/source", selected: "1.0.0", want: context.DeadlineExceeded},
	} {
		snapshot := newServiceSnapshot()
		cause := error(context.DeadlineExceeded)
		if test.operation == "release" {
			cause = postgres.ErrNotFound
		}
		snapshot.failures[test.operation] = cause
		_, err := New(snapshot).GetReleaseArtifact(t.Context(), "1.0.0", "schema", "source")
		require.ErrorIs(t, err, test.want)
		current, selected, ok := release.RequestMetadata(err)
		require.True(t, ok)
		assert.Equal(t, snapshot.current, current)
		assert.Equal(t, test.selected, selected)
	}
}

// TestMappingAssemblyRejectsMissingProgramsAndOversizedMetadata verifies missing programs and oversized declared content fail before resource-byte loads.
// Invalid extension JSON exceeds the budget, and mismatched resource ownership fails integrity checks.
func TestMappingAssemblyRejectsMissingProgramsAndOversizedMetadata(t *testing.T) {
	snapshot := newServiceSnapshot()
	mapping, err := loadSourceMapping(t.Context(), snapshot, "1.0.0", "mapping", make(schemaMetadataCache))
	require.NoError(t, err)
	mapping.Descriptor.Resources = nil
	_, err = assembleSourceMappingPackage(t.Context(), snapshot, "1.0.0", mapping)
	require.ErrorIs(t, err, postgres.ErrInconsistentState)
	assert.Zero(t, snapshot.resourceReads)
	mapping.Descriptor.Resources = append([]model.Resource(nil), snapshot.resources["mapping-r1"]...)
	mapping.Descriptor.Resources[0].Size = limits.MaxGeneratedJSONBytes
	_, err = assembleSourceMappingPackage(t.Context(), snapshot, "1.0.0", mapping)
	require.ErrorIs(t, err, postgres.ErrInconsistentState)
	assert.Zero(t, snapshot.resourceReads, "oversized program metadata must fail before loading program bytes")
	assert.Greater(t, extensionBytes([]byte("{")), limits.MaxGeneratedJSONBytes)
	require.ErrorIs(t, EnsureResourceOwner(model.Resource{ArtifactRevision: "other"}, "expected"), postgres.ErrInconsistentState)
}

// TestDescriptorReadsPreserveCompleteMetadata verifies complete selected and immutable descriptors, release-scoped links, exact content, URI resolution, and ownership guards.
func TestDescriptorReadsPreserveCompleteMetadata(t *testing.T) {
	snapshot := newServiceSnapshot()
	service := New(snapshot)
	selected, err := service.ReleaseDescriptor(t.Context(), "1.0.0", "source-mapping", "mapping")
	require.NoError(t, err)
	assert.Equal(t, snapshot.artifacts["mapping-r1"], selected.Value.Artifact)
	assert.Equal(t, snapshot.resources["mapping-r1"], selected.Value.Resources)
	assert.Equal(t, snapshot.dependencies["mapping-r1"], selected.Value.Dependencies)
	assert.Equal(t, "source", selected.Value.SourceArtifactType)
	immutable, err := service.ImmutableDescriptor(t.Context(), "mapping-r1")
	require.NoError(t, err)
	assert.Equal(t, selected.Value.Artifact, immutable.Value.Artifact)
	assert.Empty(t, immutable.Value.SourceArtifactType, "immutable metadata must not contain release-dependent links")
	assert.Empty(t, immutable.Selected)
	page, err := service.List(t.Context(), "1.0.0", model.ArtifactFilter{}, nil, nil, 2)
	require.NoError(t, err)
	assert.Equal(t, snapshot.current, page.Current)
	assert.Equal(t, []Descriptor{selected.Value}, page.Items)
	value, err := service.GetArtifact(t.Context(), "source-r1")
	require.NoError(t, err)
	assert.Equal(t, snapshot.artifacts["source-r1"], value.Value)
	resource, err := service.GetResource(t.Context(), "mapping-r1", "program.bin")
	require.NoError(t, err)
	assert.Equal(t, snapshot.resources["mapping-r1"][0], resource.Value)
	uri, err := service.ResolveURI(t.Context(), "1.0.0", "https://example.test/schema")
	require.NoError(t, err)
	assert.Equal(t, "source-r1", uri.Value.ArtifactRevision)
	assert.Equal(t, "1.0.0", uri.Selected)
	require.NoError(t, ValidateCanonical(value.Value, "schema", "source", "source-r1"))
	require.ErrorIs(t, ValidateCanonical(value.Value, "schema", "target", "source-r1"), postgres.ErrNotFound)
	require.NoError(t, EnsureResourceOwner(resource.Value, "mapping-r1"))
}

// TestMappingCollectionsAndMetadataUseCompleteSnapshots verifies collections share enriched metadata and complete packages while lookahead sentinels remain metadata-only.
func TestMappingCollectionsAndMetadataUseCompleteSnapshots(t *testing.T) {
	snapshot := newServiceSnapshot()
	service := New(snapshot)
	metadata, err := service.SourceMappingMetadata(t.Context(), "1.0.0", "mapping")
	require.NoError(t, err)
	last := "before-mapping"
	items, err := service.SourceMappings(t.Context(), "1.0.0", model.ArtifactFilter{Kind: "source-mapping", SourceArtifactType: "source", TargetArtifactType: "target"}, &last, 2)
	require.NoError(t, err)
	assert.Equal(t, []model.SourceMapping{metadata.Value}, items.Value)
	packages, err := service.SourceMappingPackages(t.Context(), "1.0.0", model.ArtifactFilter{SourceArtifactType: "source"}, &last, 2)
	require.NoError(t, err)
	require.Len(t, packages.Value, 1)
	assert.Equal(t, metadata.Value.Descriptor, packages.Value[0].Mapping)
	assert.Len(t, packages.Value[0].Programs, 1)
	// The extra keyset item is a lookahead sentinel, not an eagerly assembled package.
	sentinel, err := service.SourceMappingPackages(t.Context(), "1.0.0", model.ArtifactFilter{SourceArtifactType: "source"}, nil, 1)
	require.NoError(t, err)
	require.Len(t, sentinel.Value, 1)
	assert.Empty(t, sentinel.Value[0].Programs)
	assert.Equal(t, "mapping-r1", sentinel.Value[0].Mapping.Artifact.Revision)
}

// TestHistoryBindsSnapshotCeilingAndRejectsMissingHistory verifies explicit continuation ceilings override current state and absent logical history returns ErrNotFound.
func TestHistoryBindsSnapshotCeilingAndRejectsMissingHistory(t *testing.T) {
	snapshot := newServiceSnapshot()
	snapshot.history = []model.ReleaseArtifact{{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "source", ArtifactRevision: "source-r1", ActivationSequence: 1}}
	ceiling := int64(7)
	result, err := New(snapshot).History(t.Context(), "schema", "source", nil, &ceiling, 2)
	require.NoError(t, err)
	assert.Equal(t, snapshot.history, result.Value)
	assert.Equal(t, ceiling, result.Current.SnapshotSequence)
	assert.Equal(t, ceiling, snapshot.historyMaximum)
	snapshot.history = nil
	_, err = New(snapshot).History(t.Context(), "schema", "source", nil, nil, 2)
	require.ErrorIs(t, err, postgres.ErrNotFound)
	assert.Equal(t, snapshot.current.SnapshotSequence, snapshot.historyMaximum)
}

// TestArtifactOperationsPropagateLateFailures verifies each descriptor, collection, history, content, and catalog operation preserves late causes within one snapshot.
func TestArtifactOperationsPropagateLateFailures(t *testing.T) {
	for _, test := range []struct {
		name, failure string
		read          func(context.Context, *Service) error
	}{
		{name: "descriptor dependencies", failure: "dependencies/mapping-r1", read: func(ctx context.Context, s *Service) error {
			_, err := s.ReleaseDescriptor(ctx, "1.0.0", "source-mapping", "mapping")
			return err
		}},
		{name: "descriptor mapping", failure: "mapping", read: func(ctx context.Context, s *Service) error {
			_, err := s.ReleaseDescriptor(ctx, "1.0.0", "source-mapping", "mapping")
			return err
		}},
		{name: "immutable resources", failure: "resources/source-r1", read: func(ctx context.Context, s *Service) error {
			_, err := s.ImmutableDescriptor(ctx, "source-r1")
			return err
		}},
		{name: "list", failure: "list", read: func(ctx context.Context, s *Service) error {
			_, err := s.List(ctx, "1.0.0", model.ArtifactFilter{}, nil, nil, 2)
			return err
		}},
		{name: "list resources", failure: "resources/mapping-r1", read: func(ctx context.Context, s *Service) error {
			_, err := s.List(ctx, "1.0.0", model.ArtifactFilter{}, nil, nil, 2)
			return err
		}},
		{name: "list dependencies", failure: "dependencies/mapping-r1", read: func(ctx context.Context, s *Service) error {
			_, err := s.List(ctx, "1.0.0", model.ArtifactFilter{}, nil, nil, 2)
			return err
		}},
		{name: "list mapping", failure: "mapping", read: func(ctx context.Context, s *Service) error {
			_, err := s.List(ctx, "1.0.0", model.ArtifactFilter{}, nil, nil, 2)
			return err
		}},
		{name: "mappings list", failure: "list", read: func(ctx context.Context, s *Service) error {
			_, err := s.SourceMappings(ctx, "1.0.0", model.ArtifactFilter{}, nil, 2)
			return err
		}},
		{name: "mappings load", failure: "resources/mapping-r1", read: func(ctx context.Context, s *Service) error {
			_, err := s.SourceMappings(ctx, "1.0.0", model.ArtifactFilter{}, nil, 2)
			return err
		}},
		{name: "packages list", failure: "list", read: func(ctx context.Context, s *Service) error {
			_, err := s.SourceMappingPackages(ctx, "1.0.0", model.ArtifactFilter{SourceArtifactType: "source"}, nil, 2)
			return err
		}},
		{name: "history", failure: "history", read: func(ctx context.Context, s *Service) error {
			_, err := s.History(ctx, "schema", "source", nil, nil, 2)
			return err
		}},
		{name: "artifact", failure: "revision/source-r1", read: func(ctx context.Context, s *Service) error { _, err := s.GetArtifact(ctx, "source-r1"); return err }},
		{name: "resource", failure: "resource", read: func(ctx context.Context, s *Service) error {
			_, err := s.GetResource(ctx, "mapping-r1", "program.bin")
			return err
		}},
		{name: "URI", failure: "uri", read: func(ctx context.Context, s *Service) error {
			_, err := s.ResolveURI(ctx, "1.0.0", "https://example.test/schema")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := newServiceSnapshot()
			cause := errors.New("late operation failure")
			snapshot.failures[test.failure] = cause
			err := test.read(t.Context(), New(snapshot))
			require.ErrorIs(t, err, cause)
			assert.Equal(t, 1, snapshot.reads)
		})
	}
}
