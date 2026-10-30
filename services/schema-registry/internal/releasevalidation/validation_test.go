// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package releasevalidation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/representation"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/source-mapping"
)

const testOrigin = "https://registry.example.test"

func TestValidateRejectsOversizedMappingPackageAfterFormatValidation(t *testing.T) {
	value, context := mappingClosure(t)
	// The second mapping proves acceptance checks every selected candidate.
	oversizeMapping(t, &value, context, "mapping-b")
	require.NoError(t, value.Validate(context), "format validity does not establish response serviceability")
	err := Validate(value, context)
	require.ErrorIs(t, err, representation.ErrTooLarge)
	require.NotErrorIs(t, err, manifest.ErrInvalid)
	require.ErrorContains(t, err, "mapping-b")
}

func TestValidateExcludesLargeAncillaryContent(t *testing.T) {
	value, context := mappingClosure(t)
	key := mappingKey("mapping-a")
	item := context.Artifacts[key]
	data := bytes.Repeat([]byte{255}, artifact.MaxArtifactBytes)
	for _, name := range []string{"ancillary-a.bin", "ancillary-b.bin", "ancillary-c.bin"} {
		item.Resources = append(item.Resources, testResource(name, "metadata", data))
	}
	updateArtifact(&value, context, item)
	require.NoError(t, value.Validate(context))
	require.NoError(t, Validate(value, context))
	closure := indexClosure(value, context)
	selection := closure.selections[key]
	pack, err := closure.mappingPackage(selection)
	require.NoError(t, err)
	require.Len(t, pack.Mapping.Resources, 5, "ancillary metadata remains in the descriptor")
	require.Len(t, pack.Programs, 2, "ancillary content does not enter the package")
	size, err := representation.MappingPackageSize(pack, value.SchemaVersion)
	require.NoError(t, err)
	require.Less(t, size, int64(1<<20))
	for _, resource := range pack.Mapping.Resources {
		require.Nil(t, resource.Bytes, "size validation does not retain resource content")
	}
}

func TestValidateDeduplicatesLargeSharedProgram(t *testing.T) {
	value, context := mappingClosure(t)
	item := context.Artifacts[mappingKey("mapping-a")]
	program := testResource("shared.bin", "program", bytes.Repeat([]byte{255}, artifact.MaxArtifactBytes))
	item.Resources = []artifact.Resource{program}
	rewriteMapping(t, &item, func(document map[string]any) {
		programs := document["programs"].(map[string]any)
		programs["condition"] = programReference(program)
		for _, raw := range programs["transformations"].([]any) {
			transformation := raw.(map[string]any)
			transformation["programRef"], transformation["programDigest"] = program.Name, program.Digest
		}
	})
	updateArtifact(&value, context, item)
	require.NoError(t, value.Validate(context))
	require.NoError(t, Validate(value, context))
	closure := indexClosure(value, context)
	pack, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	require.Len(t, pack.Programs, 1)
	require.Nil(t, pack.Programs[0].Bytes)
	size, err := representation.MappingPackageSize(pack, value.SchemaVersion)
	require.NoError(t, err)
	require.Greater(t, size, int64(artifact.MaxArtifactBytes))
	require.Less(t, size, int64(representation.MaxGeneratedJSONBytes))
}

func TestMappingPackagePreservesValidatedClosure(t *testing.T) {
	value, context := mappingClosure(t)
	require.NoError(t, Validate(value, context))
	closure := indexClosure(value, context)
	pack, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	require.Equal(t, []string{"source", "edge", "entity"}, schemaTypes(pack.Schemas))
	require.Equal(t, []string{"a.bin", "z.bin"}, []string{pack.Programs[0].Name, pack.Programs[1].Name})
	require.Equal(t, []string{"data-center-inventory", "insights-repository", "telemetry-store"}, storeNames(pack.Stores))
	require.Equal(t, "source", pack.Mapping.SourceArtifactType)
	require.Equal(t, "source-r1", pack.Detail.SourceRevision)
	require.Equal(t, "z.bin", pack.Detail.Condition.ProgramName)
	require.Equal(t, "opaque", pack.Detail.EngineLanguage)
	require.Equal(t, "1.0.0", pack.Detail.EngineLanguageVersion)
	require.Equal(t, "opaque-profile", pack.Detail.ExecutionProfile)
	require.Equal(t, []model.MappingTransformation{
		{ProgramBinding: model.ProgramBinding{ProgramName: "a.bin", ProgramDigest: pack.Programs[0].Digest}, TargetArtifactType: "entity", TargetRevision: "entity-r1", Cardinality: "one"},
		{ProgramBinding: model.ProgramBinding{ProgramName: "a.bin", ProgramDigest: pack.Programs[0].Digest}, TargetArtifactType: "edge", TargetRevision: "edge-r1", Cardinality: "many"},
		{ProgramBinding: model.ProgramBinding{ProgramName: "a.bin", ProgramDigest: pack.Programs[0].Digest}, TargetArtifactType: "entity", TargetRevision: "entity-r1", Cardinality: "one"},
	}, pack.Detail.Transformations)
	require.Equal(t, "1.0.0", pack.Detail.TargetSchemaVersion)
	require.JSONEq(t, `{"org.example.test":{"note":"<accepted>&","values":[true,1]}}`, string(pack.Mapping.Artifact.Extensions))
	// Boolean schema documents retain their JSON values instead of becoming objects.
	require.Equal(t, []byte("true"), pack.Schemas[0].Artifact.Payload.Bytes)
	require.Equal(t, []byte("false"), pack.Schemas[1].Artifact.Payload.Bytes)
	require.Equal(t, artifact.SchemaMediaType, pack.Schemas[1].Artifact.Payload.MediaType)
	require.Equal(t, "edge-r1", pack.Schemas[1].Artifact.Revision)
	require.Equal(t, "source-r1", pack.Mapping.Dependencies[2].Revision)
}

func TestMappingPackageSizeMatchesSharedRepresentation(t *testing.T) {
	value, context := mappingClosure(t)
	require.NoError(t, value.Validate(context))
	closure := indexClosure(value, context)
	pack, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	size, err := representation.MappingPackageSize(pack, value.SchemaVersion)
	require.NoError(t, err)
	// Only the shared response builder needs program bytes for Base64 encoding.
	for index := range pack.Programs {
		for _, resource := range context.Artifacts[mappingKey("mapping-a")].Resources {
			if resource.Name == pack.Programs[index].Name {
				pack.Programs[index].Bytes = resource.Bytes
			}
		}
	}
	body, err := representation.BuildMappingPackage(pack, value.SchemaVersion)
	require.NoError(t, err)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.EqualValues(t, len(encoded), size)
}

func TestValidateAllowsCanonicalSourceAsTarget(t *testing.T) {
	value, context := mappingClosure(t)
	item := context.Artifacts[mappingKey("mapping-a")]
	rewriteMapping(t, &item, func(document map[string]any) {
		document["sourceSchema"] = map[string]any{"id": "entity", "version": value.SchemaVersion}
	})
	updateArtifact(&value, context, item)
	require.NoError(t, Validate(value, context))
	closure := indexClosure(value, context)
	pack, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	require.Equal(t, []string{"entity", "edge"}, schemaTypes(pack.Schemas))
	require.Equal(t, "entity-r1", pack.Detail.SourceRevision)
	require.Equal(t, []string{"data-center-inventory", "insights-repository", "telemetry-store"}, storeNames(pack.Stores))
}

func TestValidateAllowsMappingWithoutCondition(t *testing.T) {
	value, context := mappingClosure(t)
	item := context.Artifacts[mappingKey("mapping-a")]
	item.Resources = item.Resources[:1]
	rewriteMapping(t, &item, func(document map[string]any) { delete(document["programs"].(map[string]any), "condition") })
	updateArtifact(&value, context, item)
	require.NoError(t, Validate(value, context))
	closure := indexClosure(value, context)
	pack, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	require.Nil(t, pack.Detail.Condition)
	require.Len(t, pack.Programs, 1)
}

func TestValidateAllowsReleaseWithoutMappings(t *testing.T) {
	value, context := mappingClosure(t)
	value.Artifacts = value.Artifacts[:3]
	value.URICatalog = value.URICatalog[:3]
	require.NoError(t, Validate(value, context))
	value.FormatVersion = "unsupported"
	require.ErrorIs(t, Validate(value, context), manifest.ErrInvalid)
}

func TestValidateRejectsInvalidFormatBeforeApplicationSize(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifest.Manifest, manifest.Context)
		want   string
	}{
		{"missing artifact", func(_ *manifest.Manifest, context manifest.Context) {
			delete(context.Artifacts, manifest.Key{Kind: artifact.KindSchema, Type: "entity", Revision: "entity-r1"})
		}, "bytes are unavailable"},
		{"missing dependency", func(value *manifest.Manifest, context manifest.Context) {
			item := context.Artifacts[mappingKey("mapping-b")]
			item.Dependencies = item.Dependencies[1:]
			updateArtifact(value, context, item)
		}, "missing resolved schema dependency"},
		{"missing resource", func(value *manifest.Manifest, context manifest.Context) {
			item := context.Artifacts[mappingKey("mapping-b")]
			item.Resources = item.Resources[:1]
			updateArtifact(value, context, item)
		}, "program binding does not match resource"},
		{"corrupt program bytes", func(_ *manifest.Manifest, context manifest.Context) {
			key := mappingKey("mapping-b")
			item := context.Artifacts[key]
			item.Resources[0].Bytes = []byte("corrupt")
			context.Artifacts[key] = item
		}, "descriptor size does not match bytes"},
		{"corrupt condition binding", func(value *manifest.Manifest, context manifest.Context) {
			item := context.Artifacts[mappingKey("mapping-b")]
			rewriteMapping(t, &item, func(document map[string]any) {
				document["programs"].(map[string]any)["condition"].(map[string]any)["programDigest"] = testDescriptor([]byte("different"), "application/octet-stream", "different").Digest
			})
			updateArtifact(value, context, item)
		}, "program binding does not match resource"},
		{"unresolved context binding", func(value *manifest.Manifest, _ manifest.Context) {
			value.SchemaContextBindings = []manifest.Binding{{SchemaRevision: "entity-r1", ContextRevisions: []string{"missing-r1"}}}
		}, "binding context is unresolved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, context := mappingClosure(t)
			// An invalid later mapping wins over an earlier oversized package.
			oversizeMapping(t, &value, context, "mapping-a")
			test.mutate(&value, context)
			err := Validate(value, context)
			require.ErrorIs(t, err, manifest.ErrInvalid)
			require.NotErrorIs(t, err, representation.ErrTooLarge)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestMappingPackageRejectsMissingValidatedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*selectedClosure)
		want   string
	}{
		{"missing selection", func(c *selectedClosure) { delete(c.selections, mappingKey("mapping-a")) }, "selection is missing"},
		{"missing immutable bytes", func(c *selectedClosure) { delete(c.context.Artifacts, mappingKey("mapping-a")) }, "bytes are missing"},
		{"missing owned resource", func(c *selectedClosure) {
			key := mappingKey("mapping-a")
			item := c.context.Artifacts[key]
			item.Resources = nil
			c.context.Artifacts[key] = item
		}, "resource a.bin is missing"},
		{"missing source schema", func(c *selectedClosure) { delete(c.schemas, "source") }, "selected schema source/"},
		{"missing target schema", func(c *selectedClosure) { delete(c.schemas, "entity") }, "selected schema entity/"},
		{"missing schema bytes", func(c *selectedClosure) {
			delete(c.context.Artifacts, manifest.Key{Kind: artifact.KindSchema, Type: "entity", Revision: "entity-r1"})
		}, "bytes are missing"},
		{"missing condition program", func(c *selectedClosure) {
			key := mappingKey("mapping-a")
			item := c.selections[key]
			item.Resources = item.Resources[:1]
			c.selections[key] = item
		}, "program resource z.bin is missing"},
		{"missing transformation program", func(c *selectedClosure) {
			key := mappingKey("mapping-a")
			item := c.selections[key]
			item.Resources = item.Resources[1:]
			c.selections[key] = item
		}, "program resource a.bin is missing"},
		{"missing stores", func(c *selectedClosure) { delete(c.stores, "entity") }, "stores are missing"},
		{"unrepresentable extensions", func(c *selectedClosure) {
			key := mappingKey("mapping-a")
			item := c.selections[key]
			item.Extensions = map[string]any{"org.example.test": make(chan int)}
			c.selections[key] = item
		}, "artifact extensions"},
		{"invalid descriptor", func(c *selectedClosure) {
			key := mappingKey("mapping-a")
			item := c.context.Artifacts[key]
			item.PayloadBytes = []byte("{")
			c.context.Artifacts[key] = item
		}, "parse mapping descriptor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, context := mappingClosure(t)
			require.NoError(t, value.Validate(context))
			closure := indexClosure(value, context)
			selection := closure.selections[mappingKey("mapping-a")]
			test.mutate(closure)
			_, err := closure.mappingPackage(selection)
			require.ErrorIs(t, err, representation.ErrInvalid)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func mappingKey(name string) manifest.Key {
	return manifest.Key{Kind: artifact.KindSourceMapping, Type: name, Revision: name + "-r1"}
}

func mappingClosure(t testing.TB) (manifest.Manifest, manifest.Context) {
	t.Helper()
	value := manifest.Manifest{
		Schema: "https://example.test/manifest-1.0.0", FormatVersion: manifest.FormatVersion, SchemaVersion: "1.0.0",
		Source: manifest.Source{Repository: "https://git.example.test/model", Commit: strings.Repeat("a", 40)},
	}
	context := manifest.Context{RegistryOrigin: testOrigin, Artifacts: make(map[manifest.Key]artifact.Artifact)}
	for _, definition := range []struct {
		name  string
		class artifact.SchemaClass
		data  string
	}{{"edge", artifact.SchemaClassEdge, "false"}, {"entity", artifact.SchemaClassEntity, `{"type":"object"}`}, {"source", artifact.SchemaClassSource, "true"}} {
		item := artifact.Artifact{
			Kind: artifact.KindSchema, Type: definition.name, Revision: definition.name + "-r1", CreatedUnder: "1.0.0",
			SchemaClass: definition.class, PayloadBytes: []byte(definition.data),
			Payload: testDescriptor([]byte(definition.data), artifact.SchemaMediaType, "schemas/"+definition.name+".json"),
		}
		item.CanonicalURI = testOrigin + "/v1/resources/schema/" + item.Type + "/" + item.Revision
		updateArtifact(&value, context, item)
		value.URICatalog = append(value.URICatalog, manifest.URIEntry{URI: item.CanonicalURI, ArtifactRevision: item.Revision})
	}
	value.StorageCatalog = []manifest.StorageEntry{
		{Kind: artifact.KindSchema, ArtifactType: "edge", Stores: []string{"data-center-inventory", "telemetry-store"}},
		{Kind: artifact.KindSchema, ArtifactType: "entity", Stores: []string{"insights-repository", "telemetry-store"}},
	}
	for _, name := range []string{"mapping-a", "mapping-b"} {
		program := testResource("a.bin", "program", []byte{0, 255})
		condition := testResource("z.bin", "program", []byte("condition"))
		transform := func(target, cardinality string, version bool) map[string]any {
			reference := map[string]any{"id": target, "revision": target + "-r1"}
			if version {
				reference = map[string]any{"id": target, "version": value.SchemaVersion}
			}
			entry := programReference(program)
			entry["targetSchema"], entry["cardinality"] = reference, cardinality
			return entry
		}
		payload, err := json.Marshal(map[string]any{
			"$schema": "https://example.test/mapping-1.0.0", "formatVersion": "1.0.0", "id": name,
			"sourceSchema": map[string]any{"id": "source", "revision": "source-r1"},
			"engine":       map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque-profile"},
			"programs": map[string]any{"condition": programReference(condition), "transformations": []any{
				transform("entity", "one", false), transform("edge", "many", true), transform("entity", "one", false),
			}},
		})
		require.NoError(t, err)
		item := artifact.Artifact{
			Kind: artifact.KindSourceMapping, Type: name, Revision: name + "-r1", CreatedUnder: "1.0.0", TargetSchema: "1.0.0",
			PayloadBytes: payload, Payload: testDescriptor(payload, sourcemapping.DescriptorMediaType, "mappings/"+name+".json"),
			CanonicalURI: testOrigin + "/v1/resources/source-mapping/" + name + "/" + name + "-r1",
			Resources:    []artifact.Resource{program, condition},
			Dependencies: []artifact.Dependency{
				{Kind: artifact.KindSchema, Type: "edge", Revision: "edge-r1"},
				{Kind: artifact.KindSchema, Type: "entity", Revision: "entity-r1"},
				{Kind: artifact.KindSchema, Type: "source", Revision: "source-r1"},
			},
			Extensions: map[string]any{"org.example.test": map[string]any{"note": "<accepted>&", "values": []any{true, 1}}},
		}
		updateArtifact(&value, context, item)
		value.URICatalog = append(value.URICatalog, manifest.URIEntry{URI: item.CanonicalURI, ArtifactRevision: item.Revision})
	}
	return value, context
}

func updateArtifact(value *manifest.Manifest, context manifest.Context, item artifact.Artifact) {
	sort.Slice(item.Resources, func(i, j int) bool {
		left, right := item.Resources[i], item.Resources[j]
		if left.Role == right.Role {
			return left.Name < right.Name
		}
		return left.Role < right.Role
	})
	key := manifest.Key{Kind: item.Kind, Type: item.Type, Revision: item.Revision}
	context.Artifacts[key] = item
	resources := append([]artifact.Resource{}, item.Resources...)
	for index := range resources {
		resources[index].Bytes = nil
	}
	selection := manifest.Artifact{
		Kind: item.Kind, Type: item.Type, Revision: item.Revision, CreatedUnderSchemaVersion: item.CreatedUnder,
		SchemaClass: item.SchemaClass, TargetSchema: item.TargetSchema, CanonicalURI: item.CanonicalURI,
		Payload: item.Payload, Resources: resources, Dependencies: item.Dependencies, Extensions: item.Extensions,
	}
	for index, previous := range value.Artifacts {
		if previous.Kind == item.Kind && previous.Type == item.Type {
			value.Artifacts[index] = selection
			return
		}
	}
	value.Artifacts = append(value.Artifacts, selection)
}

func testResource(name, role string, data []byte) artifact.Resource {
	return artifact.Resource{Name: name, Role: role, Descriptor: testDescriptor(data, "application/octet-stream", "resources/"+name), Bytes: data}
}

func testDescriptor(data []byte, mediaType, path string) artifact.Descriptor {
	digest := sha256.Sum256(data)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: path}
}

func programReference(resource artifact.Resource) map[string]any {
	return map[string]any{"programRef": resource.Name, "programDigest": resource.Digest}
}

func rewriteMapping(t testing.TB, item *artifact.Artifact, mutate func(map[string]any)) {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(item.PayloadBytes, &document))
	mutate(document)
	payload, err := json.Marshal(document)
	require.NoError(t, err)
	item.PayloadBytes = payload
	item.Payload = testDescriptor(payload, item.Payload.MediaType, item.Payload.BundlePath)
}

func oversizeMapping(t testing.TB, value *manifest.Manifest, context manifest.Context, name string) {
	t.Helper()
	item := context.Artifacts[mappingKey(name)]
	data := bytes.Repeat([]byte{255}, artifact.MaxArtifactBytes)
	item.Resources = []artifact.Resource{
		testResource("a.bin", "program", data), testResource("b.bin", "program", data), testResource("c.bin", "program", data),
	}
	rewriteMapping(t, &item, func(document map[string]any) {
		programs := document["programs"].(map[string]any)
		programs["condition"] = programReference(item.Resources[2])
		for index, raw := range programs["transformations"].([]any) {
			transformation := raw.(map[string]any)
			program := item.Resources[index%2]
			transformation["programRef"], transformation["programDigest"] = program.Name, program.Digest
		}
	})
	updateArtifact(value, context, item)
}

func schemaTypes(schemas []model.Descriptor) []string {
	result := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		result = append(result, schema.Artifact.Type)
	}
	return result
}

func storeNames(stores []model.StorageEntry) []string {
	result := make([]string, 0, len(stores))
	for _, store := range stores {
		result = append(result, store.Store)
	}
	return result
}
