package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mappingPayload builds a descriptor whose condition and mixed canonical outputs share one exact program resource.
func mappingPayload(t testing.TB, name string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"$schema":       "https://example.test/mapping-1.0.0",
		"formatVersion": "1.0.0",
		"id":            name,
		"sourceSchema":  map[string]any{"id": "source", "revision": "source-r1"},
		"engine":        map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque"},
		"programs": map[string]any{
			"condition": map[string]any{"programRef": "shared.bin", "programDigest": digest([]byte{0, 255})},
			"transformations": []any{
				map[string]any{
					"programRef":    "shared.bin",
					"programDigest": digest([]byte{0, 255}),
					"cardinality":   "one",
					"targetSchema":  map[string]any{"id": "entity", "revision": "entity-r1"},
				},
				map[string]any{
					"programRef":    "shared.bin",
					"programDigest": digest([]byte{0, 255}),
					"cardinality":   "many",
					"targetSchema":  map[string]any{"id": "edge", "version": "1.0.0"},
				},
			},
		},
	})
	require.NoError(t, err)
	return data
}

// mappingClosure supplies two candidate mappings, their shared source schema, canonical targets, catalog entries, and exact bytes.
func mappingClosure(t testing.TB) (Manifest, Context) {
	t.Helper()

	m := Manifest{
		Schema:        "https://example.test/manifest-1.0.0",
		FormatVersion: "1.0.0",
		SchemaVersion: "1.0.0",
		Source:        Source{Repository: "https://git.example.test/model", Commit: strings.Repeat("a", 40)},
	}
	context := Context{RegistryOrigin: testOrigin, Artifacts: make(map[artifactmodel.Dependency]artifactmodel.Artifact)}

	for _, definition := range []struct {
		name  string
		class artifactmodel.SchemaClass
	}{
		{"edge", artifactmodel.SchemaClassEdge},
		{"entity", artifactmodel.SchemaClassEntity},
		{"source", artifactmodel.SchemaClassSource},
		{"mapping-a", ""},
		{"mapping-b", ""},
	} {
		item := artifactmodel.Artifact{
			Kind:                      artifactmodel.KindSchema,
			Type:                      definition.name,
			Revision:                  definition.name + "-r1",
			CreatedUnderSchemaVersion: "1.0.0",
			SchemaClass:               definition.class,
			PayloadBytes:              []byte("true"),
		}

		if definition.class == "" {
			item.Kind, item.TargetSchema = artifactmodel.KindSourceMapping, "1.0.0"
			item.PayloadBytes = mappingPayload(t, item.Type)
			item.Resources = []artifactmodel.Resource{
				{
					Name:       "shared.bin",
					Role:       "program",
					Descriptor: descriptor([]byte{0, 255}, "application/octet-stream", "mappings/"+item.Type+"/shared.bin"),
					Bytes:      []byte{0, 255},
				},
			}
			item.Dependencies = []artifactmodel.Dependency{
				{Kind: artifactmodel.KindSchema, Type: "edge", Revision: "edge-r1"},
				{Kind: artifactmodel.KindSchema, Type: "entity", Revision: "entity-r1"},
				{Kind: artifactmodel.KindSchema, Type: "source", Revision: "source-r1"},
			}
		}

		mediaType := artifactmodel.SchemaMediaType
		if item.Kind == artifactmodel.KindSourceMapping {
			mediaType = "application/vnd.apeiro.ocg.source-mapping.v1+json"
		}

		item.Payload = descriptor(item.PayloadBytes, mediaType, "artifacts/"+item.Type+".json")
		item.CanonicalURI = testOrigin + "/v1/resources/" + string(item.Kind) + "/" + item.Type + "/" + item.Revision
		context.Artifacts[artifactmodel.Dependency{Kind: item.Kind, Type: item.Type, Revision: item.Revision}] = item

		m.Artifacts = append(m.Artifacts, item.Metadata())

		m.URICatalog = append(m.URICatalog, URIEntry{URI: item.CanonicalURI, ArtifactRevision: item.Revision})

		if item.Kind == artifactmodel.KindSchema && item.SchemaClass != artifactmodel.SchemaClassSource {
			storageEntry := StorageEntry{
				Kind:         artifactmodel.KindSchema,
				ArtifactType: item.Type,
				Stores:       []Store{StoreDataCenterInventory},
			}
			m.StorageCatalog = append(m.StorageCatalog, storageEntry)
		}
	}
	return m, context
}

// TestMultiCandidateMappingClosure checks candidate coexistence and rejection of missing targets, invalid stores, or corrupt program bindings.
func TestMultiCandidateMappingClosure(t *testing.T) {
	m, context := mappingClosure(t)
	require.NoError(t, m.Validate(context))
	for _, mutate := range []func(*Manifest, Context){
		func(m *Manifest, _ Context) { m.StorageCatalog = m.StorageCatalog[1:] },
		func(m *Manifest, _ Context) { m.Artifacts = m.Artifacts[1:] },
		func(_ *Manifest, c Context) {
			key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
			item := c.Artifacts[key]
			item.Resources[0].Bytes = []byte("corrupt")
			c.Artifacts[key] = item
		},
		func(m *Manifest, c Context) {
			key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
			item := c.Artifacts[key]

			resource := artifactmodel.Resource{
				Name:       "unreferenced.bin",
				Role:       "program",
				Descriptor: descriptor(nil, "application/octet-stream", "extra/program.bin"),
			}
			item.Resources = append(item.Resources, resource)

			c.Artifacts[key] = item
			m.Artifacts[3].Resources = item.Metadata().Resources
		},
	} {
		m, context := mappingClosure(t)
		mutate(&m, context)
		require.Error(t, m.Validate(context))
	}
}

// TestMatchesSelectionChecksAllMetadata rejects changes to every shared artifact metadata field.
func TestMatchesSelectionChecksAllMetadata(t *testing.T) {
	m, context := mappingClosure(t)
	selection := m.Artifacts[3]
	bound := context.Artifacts[artifactmodel.Dependency{Kind: selection.Kind, Type: selection.Type, Revision: selection.Revision}]
	require.True(t, matchesSelection(selection, bound))
	for _, test := range []struct {
		name   string
		mutate func(*artifactmodel.Artifact)
	}{
		{"kind", func(a *artifactmodel.Artifact) { a.Kind = artifactmodel.KindContext }},
		{"type", func(a *artifactmodel.Artifact) { a.Type = "other" }},
		{"revision", func(a *artifactmodel.Artifact) { a.Revision = "other-r1" }},
		{"created under", func(a *artifactmodel.Artifact) { a.CreatedUnderSchemaVersion = "0.9.0" }},
		{"schema class", func(a *artifactmodel.Artifact) { a.SchemaClass = artifactmodel.SchemaClassSource }},
		{"target schema", func(a *artifactmodel.Artifact) { a.TargetSchema = "2.0.0" }},
		{"canonical URI", func(a *artifactmodel.Artifact) { a.CanonicalURI += "-other" }},
		{"payload media type", func(a *artifactmodel.Artifact) { a.Payload.MediaType = "application/octet-stream" }},
		{"payload digest", func(a *artifactmodel.Artifact) { a.Payload.Digest = digest([]byte("other")) }},
		{"payload size", func(a *artifactmodel.Artifact) { a.Payload.Size++ }},
		{"payload path", func(a *artifactmodel.Artifact) { a.Payload.BundlePath = "other/descriptor.json" }},
		{"resource name", func(a *artifactmodel.Artifact) { a.Resources[0].Name = "other.bin" }},
		{"resource role", func(a *artifactmodel.Artifact) { a.Resources[0].Role = "metadata" }},
		{"resource media type", func(a *artifactmodel.Artifact) { a.Resources[0].MediaType = "text/plain" }},
		{"resource digest", func(a *artifactmodel.Artifact) { a.Resources[0].Digest = digest([]byte("other")) }},
		{"resource size", func(a *artifactmodel.Artifact) { a.Resources[0].Size++ }},
		{"resource path", func(a *artifactmodel.Artifact) { a.Resources[0].BundlePath = "other/program.bin" }},
		{"resource removal", func(a *artifactmodel.Artifact) { a.Resources = nil }},
		{"dependencies", func(a *artifactmodel.Artifact) { a.Dependencies = nil }},
		{"extensions", func(a *artifactmodel.Artifact) { a.Extensions = map[string]any{"org.example.note": "other"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := selection.Metadata()
			test.mutate(&changed)
			assert.False(t, matchesSelection(changed, bound))
		})
	}

	selection.PayloadBytes = []byte("non-authoritative")
	selection.Resources[0].Bytes = []byte("non-authoritative")
	assert.True(t, matchesSelection(selection, bound))
	assert.Equal(t, []byte{0, 255}, bound.Resources[0].Bytes)
	selection.Dependencies, bound.Dependencies = nil, []artifactmodel.Dependency{}
	assert.True(t, matchesSelection(selection, bound))
}

// TestMappingAdditionCompatibilityUsesResolvedSourceIdentity requires a newly
// introduced source schema before classifying a mapping addition as additive.
func TestMappingAdditionCompatibilityUsesResolvedSourceIdentity(t *testing.T) {
	full, context := mappingClosure(t)
	for _, test := range []struct {
		name              string
		keepSource, bytes bool
		want              Compatibility
	}{
		{"existing source without mapping", true, true, "breaking"},
		{"new source", false, true, "additive"},
		{"unproven mapping", false, false, "breaking"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := full
			before.Artifacts = append([]artifactmodel.Artifact(nil), full.Artifacts[:2]...)
			if test.keepSource {
				before.Artifacts = append(before.Artifacts, full.Artifacts[2])
			}
			after := full
			after.Artifacts = full.Artifacts[:4]
			if !test.bytes {
				context.Artifacts = nil
			}
			changes, err := Diff(before, after, context)
			require.NoError(t, err)
			require.Equal(t, test.want, changes.Compatibility)
		})
	}
	for _, compatibility := range []struct {
		impact  Compatibility
		version string
	}{
		{CompatibilityBreaking, "2.0.0"},
		{CompatibilityAdditive, "1.1.0"},
		{CompatibilityOperational, "1.0.1"},
	} {
		require.True(t, validIncrement("1.0.0", compatibility.version, compatibility.impact))
	}
}

// TestMappingRevisionReuseExcludesPackagingButPreservesPrograms permits repacking
// while rejecting changed program digests and retired mapping identities.
func TestMappingRevisionReuseExcludesPackagingButPreservesPrograms(t *testing.T) {
	m, _ := mappingClosure(t)
	selection := m.Artifacts[3]
	history := []Release{{Manifest: m}}
	selection.Payload.BundlePath = "repacked/descriptor.json"
	selection.Resources = append([]artifactmodel.Resource(nil), selection.Resources...)
	selection.Resources[0].BundlePath = "repacked/program.bin"
	require.NoError(t, indexHistory(history).validateReuse(selection))

	selection.Resources[0].Digest = digest([]byte("different"))
	require.Error(t, indexHistory(history).validateReuse(selection))

	retired := m
	retired.Artifacts = m.Artifacts[:3]
	require.Error(t, indexHistory(append(history, Release{Manifest: retired})).validateReuse(m.Artifacts[3]))
}

// TestSharedArtifactBytesDoNotAffectMetadataComparisons preserves revision reuse and annotation-only comparison semantics.
func TestSharedArtifactBytesDoNotAffectMetadataComparisons(t *testing.T) {
	m, context := mappingClosure(t)
	selection := m.Artifacts[3]
	bound := context.Artifacts[artifactmodel.Dependency{Kind: selection.Kind, Type: selection.Type, Revision: selection.Revision}]
	require.NoError(t, indexHistory([]Release{{Manifest: m}}).validateReuse(bound))
	assert.Equal(t, revisionBoundSelection(selection), revisionBoundSelection(bound))
	assert.Equal(t, bound.PayloadBytes, context.Artifacts[artifactmodel.Dependency{Kind: bound.Kind, Type: bound.Type, Revision: bound.Revision}].PayloadBytes)
	assert.Equal(t, []byte{0, 255}, bound.Resources[0].Bytes)
	assert.Equal(t, "mappings/mapping-a/shared.bin", bound.Resources[0].BundlePath)

	left := m.Artifacts[0]
	right := left
	left.PayloadBytes, right.PayloadBytes = []byte("true"), []byte("false")
	assert.True(t, sameArtifactRelations(left, right))
	right.SchemaClass = artifactmodel.SchemaClassEntity
	assert.False(t, sameArtifactRelations(left, right))
}

// TestManifestFormatAcceptsOversizedMultiProgramPackage separates individual
// resource validity from application-level assembled-response size limits.
func TestManifestFormatAcceptsOversizedMultiProgramPackage(t *testing.T) {
	m, context := mappingClosure(t)
	key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
	item := context.Artifacts[key]
	program := bytes.Repeat([]byte{255}, schemaartifacts.MaxArtifactBytes)
	item.Resources = make([]artifactmodel.Resource, 0, 3)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		resource := artifactmodel.Resource{
			Name:       name,
			Role:       "program",
			Descriptor: descriptor(program, "application/octet-stream", "mappings/mapping-a/"+name),
			Bytes:      program,
		}
		item.Resources = append(item.Resources, resource)
	}
	var document map[string]any
	require.NoError(t, json.Unmarshal(item.PayloadBytes, &document))
	programs := document["programs"].(map[string]any)
	condition := programs["condition"].(map[string]any)
	condition["programRef"], condition["programDigest"] = "a.bin", item.Resources[0].Digest
	for index, value := range programs["transformations"].([]any) {
		transformation := value.(map[string]any)
		transformation["programRef"], transformation["programDigest"] = item.Resources[index+1].Name, item.Resources[index+1].Digest
	}
	payload, err := json.Marshal(document)
	require.NoError(t, err)
	item.PayloadBytes, item.Payload = payload, descriptor(payload, "application/vnd.apeiro.ocg.source-mapping.v1+json", "artifacts/mapping-a.json")
	context.Artifacts[key] = item
	m.Artifacts[3] = item.Metadata()
	// Format validity does not establish application response serviceability.
	require.NoError(t, m.Validate(context))
}

// TestManifestFormatAcceptsLargeAncillaryResources permits several individually bounded,
// unbound metadata resources in a valid mapping closure.
func TestManifestFormatAcceptsLargeAncillaryResources(t *testing.T) {
	m, context := mappingClosure(t)
	key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
	item := context.Artifacts[key]
	ancillary := bytes.Repeat([]byte{255}, schemaartifacts.MaxArtifactBytes)
	resources := make([]artifactmodel.Resource, 0, 4)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		resource := artifactmodel.Resource{
			Name:       name,
			Role:       "metadata",
			Descriptor: descriptor(ancillary, "application/octet-stream", "mappings/mapping-a/"+name),
			Bytes:      ancillary,
		}
		resources = append(resources, resource)
	}
	item.Resources = append(resources, item.Resources...)
	context.Artifacts[key] = item
	m.Artifacts[3].Resources = item.Metadata().Resources
	require.NoError(t, m.Validate(context))
}

// TestManifestFormatAcceptsLargeSharedProgram validates one maximum-sized
// resource referenced by both a condition and multiple transformations.
func TestManifestFormatAcceptsLargeSharedProgram(t *testing.T) {
	m, context := mappingClosure(t)
	key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
	item := context.Artifacts[key]
	program := bytes.Repeat([]byte{255}, schemaartifacts.MaxArtifactBytes)
	item.Resources[0].Bytes = program
	item.Resources[0].Descriptor = descriptor(program, "application/octet-stream", "mappings/mapping-a/shared.bin")
	var document map[string]any
	require.NoError(t, json.Unmarshal(item.PayloadBytes, &document))

	programs := document["programs"].(map[string]any)
	programs["condition"].(map[string]any)["programDigest"] = item.Resources[0].Digest
	for _, value := range programs["transformations"].([]any) {
		value.(map[string]any)["programDigest"] = item.Resources[0].Digest
	}
	payload, err := json.Marshal(document)
	require.NoError(t, err)

	item.PayloadBytes, item.Payload = payload, descriptor(payload, item.Payload.MediaType, item.Payload.BundlePath)
	context.Artifacts[key] = item
	m.Artifacts[3] = item.Metadata()
	require.NoError(t, m.Validate(context))
}

// TestManifestRejectsInvalidAncillaryResourcesAndBindings checks program-role requirements,
// reference coverage, and ancillary byte integrity.
func TestManifestRejectsInvalidAncillaryResourcesAndBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*artifactmodel.Artifact, map[string]any)
		want   string
	}{
		{"condition binds ancillary", func(item *artifactmodel.Artifact, programs map[string]any) {
			condition := programs["condition"].(map[string]any)
			condition["programRef"], condition["programDigest"] = item.Resources[0].Name, item.Resources[0].Digest
		}, "program binding does not match resource"},
		{"transformation binds ancillary", func(item *artifactmodel.Artifact, programs map[string]any) {
			transformation := programs["transformations"].([]any)[0].(map[string]any)
			transformation["programRef"], transformation["programDigest"] = item.Resources[0].Name, item.Resources[0].Digest
		}, "program binding does not match resource"},
		{"unreferenced program", func(item *artifactmodel.Artifact, _ map[string]any) { item.Resources[0].Role = "program" }, "unreferenced program resource"},
		{"corrupt ancillary bytes", func(item *artifactmodel.Artifact, _ map[string]any) { item.Resources[0].Bytes = []byte("corrupt!") }, "descriptor digest does not match bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, context := mappingClosure(t)
			key := artifactmodel.Dependency{Kind: artifactmodel.KindSourceMapping, Type: "mapping-a", Revision: "mapping-a-r1"}
			item := context.Artifacts[key]
			data := []byte("metadata")
			ancillary := artifactmodel.Resource{
				Name:       "metadata.bin",
				Role:       "metadata",
				Descriptor: descriptor(data, "application/octet-stream", "mappings/mapping-a/metadata.bin"),
				Bytes:      data,
			}
			item.Resources = append([]artifactmodel.Resource{ancillary}, item.Resources...)
			var document map[string]any
			require.NoError(t, json.Unmarshal(item.PayloadBytes, &document))

			test.mutate(&item, document["programs"].(map[string]any))
			payload, err := json.Marshal(document)
			require.NoError(t, err)

			item.PayloadBytes, item.Payload = payload, descriptor(payload, item.Payload.MediaType, item.Payload.BundlePath)
			context.Artifacts[key] = item
			m.Artifacts[3] = item.Metadata()
			require.ErrorContains(t, m.Validate(context), test.want)
		})
	}
}
