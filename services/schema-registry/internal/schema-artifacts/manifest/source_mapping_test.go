// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/stretchr/testify/require"
)

func mappingPayload(t testing.TB, name string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"$schema": "https://example.test/mapping-1.0.0", "formatVersion": "1.0.0", "id": name, "sourceSchema": map[string]any{"id": "source", "revision": "source-r1"}, "engine": map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque"}, "programs": map[string]any{"condition": map[string]any{"programRef": "shared.bin", "programDigest": digest([]byte{0, 255})}, "transformations": []any{
		map[string]any{"programRef": "shared.bin", "programDigest": digest([]byte{0, 255}), "cardinality": "one", "targetSchema": map[string]any{"id": "entity", "revision": "entity-r1"}},
		map[string]any{"programRef": "shared.bin", "programDigest": digest([]byte{0, 255}), "cardinality": "many", "targetSchema": map[string]any{"id": "edge", "version": "1.0.0"}},
	}}})
	require.NoError(t, err)
	return data
}

func mappingClosure(t testing.TB) (Manifest, Context) {
	t.Helper()
	m := Manifest{Schema: "https://example.test/manifest-1.0.0", FormatVersion: "1.0.0", SchemaVersion: "1.0.0", Source: Source{Repository: "https://git.example.test/model", Commit: strings.Repeat("a", 40)}}
	context := Context{RegistryOrigin: testOrigin, Artifacts: make(map[Key]artifact.Artifact)}
	for _, definition := range []struct {
		name  string
		class artifact.SchemaClass
	}{{"edge", artifact.SchemaClassEdge}, {"entity", artifact.SchemaClassEntity}, {"source", artifact.SchemaClassSource}, {"mapping-a", ""}, {"mapping-b", ""}} {
		item := artifact.Artifact{Kind: artifact.KindSchema, Type: definition.name, Revision: definition.name + "-r1", CreatedUnder: "1.0.0", SchemaClass: definition.class, PayloadBytes: []byte("true")}
		if definition.class == "" {
			item.Kind, item.TargetSchema = artifact.KindSourceMapping, "1.0.0"
			item.PayloadBytes = mappingPayload(t, item.Type)
			item.Resources = []artifact.Resource{{Name: "shared.bin", Role: "program", Descriptor: descriptor([]byte{0, 255}, "application/octet-stream", "mappings/"+item.Type+"/shared.bin"), Bytes: []byte{0, 255}}}
			item.Dependencies = []artifact.Dependency{{Kind: artifact.KindSchema, Type: "edge", Revision: "edge-r1"}, {Kind: artifact.KindSchema, Type: "entity", Revision: "entity-r1"}, {Kind: artifact.KindSchema, Type: "source", Revision: "source-r1"}}
		}
		mediaType := artifact.SchemaMediaType
		if item.Kind == artifact.KindSourceMapping {
			mediaType = "application/vnd.apeiro.ocg.source-mapping.v1+json"
		}
		item.Payload = descriptor(item.PayloadBytes, mediaType, "artifacts/"+item.Type+".json")
		item.CanonicalURI = testOrigin + "/v1/resources/" + string(item.Kind) + "/" + item.Type + "/" + item.Revision
		context.Artifacts[Key{item.Kind, item.Type, item.Revision}] = item
		m.Artifacts = append(m.Artifacts, Artifact{Kind: item.Kind, Type: item.Type, Revision: item.Revision, CreatedUnderSchemaVersion: item.CreatedUnder, SchemaClass: item.SchemaClass, TargetSchema: item.TargetSchema, CanonicalURI: item.CanonicalURI, Payload: item.Payload, Resources: stripResourceBytes(item.Resources), Dependencies: item.Dependencies})
		m.URICatalog = append(m.URICatalog, URIEntry{URI: item.CanonicalURI, ArtifactRevision: item.Revision})
		if item.Kind == artifact.KindSchema && item.SchemaClass != artifact.SchemaClassSource {
			m.StorageCatalog = append(m.StorageCatalog, StorageEntry{Kind: artifact.KindSchema, ArtifactType: item.Type, Stores: []string{"data-center-inventory"}})
		}
	}
	return m, context
}

func TestMultiCandidateMappingClosure(t *testing.T) {
	m, context := mappingClosure(t)
	require.NoError(t, m.Validate(context))
	for _, mutate := range []func(*Manifest, Context){
		func(m *Manifest, _ Context) { m.StorageCatalog = m.StorageCatalog[1:] },
		func(m *Manifest, _ Context) { m.Artifacts = m.Artifacts[1:] },
		func(_ *Manifest, c Context) {
			key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
			item := c.Artifacts[key]
			item.Resources[0].Bytes = []byte("corrupt")
			c.Artifacts[key] = item
		},
		func(m *Manifest, c Context) {
			key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
			item := c.Artifacts[key]
			item.Resources = append(item.Resources, artifact.Resource{Name: "unreferenced.bin", Role: "program", Descriptor: descriptor(nil, "application/octet-stream", "extra/program.bin")})
			c.Artifacts[key] = item
			m.Artifacts[3].Resources = stripResourceBytes(item.Resources)
		},
	} {
		m, context := mappingClosure(t)
		mutate(&m, context)
		require.Error(t, m.Validate(context))
	}
}

func TestMappingAdditionCompatibilityUsesResolvedSourceIdentity(t *testing.T) {
	full, context := mappingClosure(t)
	for _, test := range []struct {
		name              string
		keepSource, bytes bool
		want              string
	}{{"existing source without mapping", true, true, "breaking"}, {"new source", false, true, "additive"}, {"unproven mapping", false, false, "breaking"}} {
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
	for _, compatibility := range []struct{ impact, version string }{{"breaking", "2.0.0"}, {"additive", "1.1.0"}, {"operational", "1.0.1"}} {
		require.True(t, validIncrement("1.0.0", compatibility.version, compatibility.impact))
	}
}

func TestMappingRevisionReuseExcludesPackagingButPreservesPrograms(t *testing.T) {
	m, _ := mappingClosure(t)
	selection := m.Artifacts[3]
	history := []Release{{Manifest: m}}
	selection.Payload.BundlePath = "repacked/descriptor.json"
	selection.Resources = append([]artifact.Resource(nil), selection.Resources...)
	selection.Resources[0].BundlePath = "repacked/program.bin"
	require.NoError(t, immutableReuse(selection, history))
	selection.Resources[0].Digest = digest([]byte("different"))
	require.Error(t, immutableReuse(selection, history))
	retired := m
	retired.Artifacts = m.Artifacts[:3]
	require.Error(t, immutableReuse(m.Artifacts[3], append(history, Release{Manifest: retired})))
}

func TestManifestFormatAcceptsOversizedMultiProgramPackage(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
	item := context.Artifacts[key]
	program := bytes.Repeat([]byte{255}, config.MaxArtifactBytes)
	item.Resources = make([]artifact.Resource, 0, 3)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		item.Resources = append(item.Resources, artifact.Resource{Name: name, Role: "program", Descriptor: descriptor(program, "application/octet-stream", "mappings/mapping-a/"+name), Bytes: program})
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

func TestManifestFormatAcceptsLargeAncillaryResources(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
	item := context.Artifacts[key]
	ancillary := bytes.Repeat([]byte{255}, config.MaxArtifactBytes)
	resources := make([]artifact.Resource, 0, 4)
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		resources = append(resources, artifact.Resource{Name: name, Role: "metadata", Descriptor: descriptor(ancillary, "application/octet-stream", "mappings/mapping-a/"+name), Bytes: ancillary})
	}
	item.Resources = append(resources, item.Resources...)
	context.Artifacts[key] = item
	m.Artifacts[3].Resources = stripResourceBytes(item.Resources)
	require.NoError(t, m.Validate(context))
}

func TestManifestFormatAcceptsLargeSharedProgram(t *testing.T) {
	m, context := mappingClosure(t)
	key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
	item := context.Artifacts[key]
	program := bytes.Repeat([]byte{255}, config.MaxArtifactBytes)
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

func TestManifestRejectsInvalidAncillaryResourcesAndBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*artifact.Artifact, map[string]any)
		want   string
	}{
		{"condition binds ancillary", func(item *artifact.Artifact, programs map[string]any) {
			condition := programs["condition"].(map[string]any)
			condition["programRef"], condition["programDigest"] = item.Resources[0].Name, item.Resources[0].Digest
		}, "program binding does not match resource"},
		{"transformation binds ancillary", func(item *artifact.Artifact, programs map[string]any) {
			transformation := programs["transformations"].([]any)[0].(map[string]any)
			transformation["programRef"], transformation["programDigest"] = item.Resources[0].Name, item.Resources[0].Digest
		}, "program binding does not match resource"},
		{"unreferenced program", func(item *artifact.Artifact, _ map[string]any) { item.Resources[0].Role = "program" }, "unreferenced program resource"},
		{"corrupt ancillary bytes", func(item *artifact.Artifact, _ map[string]any) { item.Resources[0].Bytes = []byte("corrupt!") }, "descriptor digest does not match bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, context := mappingClosure(t)
			key := Key{artifact.KindSourceMapping, "mapping-a", "mapping-a-r1"}
			item := context.Artifacts[key]
			data := []byte("metadata")
			ancillary := artifact.Resource{Name: "metadata.bin", Role: "metadata", Descriptor: descriptor(data, "application/octet-stream", "mappings/mapping-a/metadata.bin"), Bytes: data}
			item.Resources = append([]artifact.Resource{ancillary}, item.Resources...)
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
