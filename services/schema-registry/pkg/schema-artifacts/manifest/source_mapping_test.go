// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
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
	context := Context{RegistryOrigin: testOrigin, Artifacts: make(map[Key]artifactmodel.Artifact)}

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
			Kind:         artifactmodel.KindSchema,
			Type:         definition.name,
			Revision:     definition.name + "-r1",
			CreatedUnder: "1.0.0",
			SchemaClass:  definition.class,
			PayloadBytes: []byte("true"),
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
		context.Artifacts[Key{item.Kind, item.Type, item.Revision}] = item

		artifact := Artifact{
			Kind:                      item.Kind,
			Type:                      item.Type,
			Revision:                  item.Revision,
			CreatedUnderSchemaVersion: item.CreatedUnder,
			SchemaClass:               item.SchemaClass,
			TargetSchema:              item.TargetSchema,
			CanonicalURI:              item.CanonicalURI,
			Payload:                   item.Payload,
			Resources:                 stripResourceBytes(item.Resources),
			Dependencies:              item.Dependencies,
		}
		m.Artifacts = append(m.Artifacts, artifact)

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
			key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
			item := c.Artifacts[key]
			item.Resources[0].Bytes = []byte("corrupt")
			c.Artifacts[key] = item
		},
		func(m *Manifest, c Context) {
			key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
			item := c.Artifacts[key]

			resource := artifactmodel.Resource{
				Name:       "unreferenced.bin",
				Role:       "program",
				Descriptor: descriptor(nil, "application/octet-stream", "extra/program.bin"),
			}
			item.Resources = append(item.Resources, resource)

			c.Artifacts[key] = item
			m.Artifacts[3].Resources = stripResourceBytes(item.Resources)
		},
	} {
		m, context := mappingClosure(t)
		mutate(&m, context)
		require.Error(t, m.Validate(context))
	}
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
			before.Artifacts = append([]Artifact(nil), full.Artifacts[:2]...)
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
	require.NoError(t, immutableReuse(selection, history))

	selection.Resources[0].Digest = digest([]byte("different"))
	require.Error(t, immutableReuse(selection, history))

	retired := m
	retired.Artifacts = m.Artifacts[:3]
	require.Error(t, immutableReuse(m.Artifacts[3], append(history, Release{Manifest: retired})))
}

// TestManifestFormatAcceptsOversizedMultiProgramPackage separates individual
// resource validity from application-level assembled-response size limits.
func TestManifestFormatAcceptsOversizedMultiProgramPackage(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
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
	m.Artifacts[3].Payload, m.Artifacts[3].Resources = item.Payload, stripResourceBytes(item.Resources)
	// Format validity does not establish application response serviceability.
	require.NoError(t, m.Validate(context))
}

// TestManifestFormatAcceptsLargeAncillaryResources permits several individually bounded,
// unbound metadata resources in a valid mapping closure.
func TestManifestFormatAcceptsLargeAncillaryResources(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
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
	m.Artifacts[3].Resources = stripResourceBytes(item.Resources)
	require.NoError(t, m.Validate(context))
}

// TestManifestFormatAcceptsLargeSharedProgram validates one maximum-sized
// resource referenced by both a condition and multiple transformations.
func TestManifestFormatAcceptsLargeSharedProgram(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
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
	m.Artifacts[3].Payload, m.Artifacts[3].Resources = item.Payload, stripResourceBytes(item.Resources)
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
			key := Key{artifactmodel.KindSourceMapping, "mapping-a", "mapping-a-r1"}
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
			m.Artifacts[3].Payload, m.Artifacts[3].Resources = item.Payload, stripResourceBytes(item.Resources)
			require.ErrorContains(t, m.Validate(context), test.want)
		})
	}
}
