// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package sourcemapping

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAcceptsOpaqueProgramAndDerivesOutputClass(t *testing.T) {
	mapping, context := validMapping(t, []byte{0, 255, 1})
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	assert.Equal(t, artifact.SchemaClassEntity, result.Transformations[0].OutputClass)
	assert.Equal(t, mapping.Resources[0], result.Transformations[0].Program)
	assert.Equal(t, "publisher language with spaces", result.Descriptor.Engine.Language)
	assert.Equal(t, "non-registry profile", result.Descriptor.Engine.ExecutionProfile)
}

func TestValidateAcceptsAncillaryResources(t *testing.T) {
	mapping, context := validMapping(t, []byte("opaque"))
	ancillary := resource("metadata.bin", []byte{0, 255})
	ancillary.Role = "metadata"
	mapping.Resources = append(mapping.Resources, ancillary)
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	assert.Equal(t, mapping.Resources[0], result.Transformations[0].Program)
}

func TestValidateRejectsInvalidAncillaryResources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*artifact.Resource)
	}{
		{"invalid name", func(r *artifact.Resource) { r.Name = "../metadata.bin" }},
		{"invalid role", func(r *artifact.Resource) { r.Role = "not a role" }},
		{"empty role", func(r *artifact.Resource) { r.Role = "" }},
		{"invalid media type", func(r *artifact.Resource) { r.MediaType = "not a media type;" }},
		{"invalid path", func(r *artifact.Resource) { r.BundlePath = "../metadata.bin" }},
		{"negative size", func(r *artifact.Resource) { r.Size = -1 }},
		{"oversized resource", func(r *artifact.Resource) { r.Size = config.MaxArtifactBytes + 1 }},
		{"size mismatch", func(r *artifact.Resource) { r.Size++ }},
		{"invalid digest", func(r *artifact.Resource) { r.Digest = "invalid" }},
		{"digest mismatch", func(r *artifact.Resource) { r.Digest = descriptor(nil, r.MediaType, r.BundlePath).Digest }},
		{"corrupt bytes", func(r *artifact.Resource) { r.Bytes = []byte("corrupt!") }},
		{"duplicate program name", func(r *artifact.Resource) { r.Name = "program.bin" }},
		{"unreferenced program", func(r *artifact.Resource) { r.Role = "program" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapping, context := validMapping(t, []byte("opaque"))
			ancillary := resource("metadata.bin", []byte("metadata"))
			ancillary.Role = "metadata"
			test.mutate(&ancillary)
			mapping.Resources = append(mapping.Resources, ancillary)
			_, err := Validate(mapping, context)
			require.Error(t, err)
		})
	}
	t.Run("duplicate ancillary name", func(t *testing.T) {
		mapping, context := validMapping(t, []byte("opaque"))
		ancillary := resource("metadata.bin", []byte("metadata"))
		ancillary.Role = "metadata"
		mapping.Resources = append(mapping.Resources, ancillary, ancillary)
		_, err := Validate(mapping, context)
		require.Error(t, err)
	})
}

func TestParseRejectsStrictJSONAndStructuralFailures(t *testing.T) {
	valid, _ := validMapping(t, []byte("opaque"))
	tests := []struct {
		name string
		data string
	}{
		{"unknown output kind", `{"$schema":"https://example.test/mapping-1.0.0","formatVersion":"1.0.0","id":"source-mapping","sourceSchema":{"id":"source","revision":"source-r1"},"targetSchema":{"id":"target","version":"2.0.0"},"cardinality":"one","outputKind":"entity-type","engine":{"language":"x","languageVersion":"1.0.0","executionProfile":"p"},"programRef":"program.bin","programDigest":"` + valid.Resources[0].Digest + `"}`},
		{"duplicate member", `{"$schema":"https://example.test/mapping-1.0.0","$schema":"https://example.test/mapping-1.0.0"}`},
		{"both selectors", `{"$schema":"https://example.test/mapping-1.0.0","formatVersion":"1.0.0","id":"source-mapping","sourceSchema":{"id":"source","revision":"source-r1","version":"2.0.0"},"targetSchema":{"id":"target","version":"2.0.0"},"cardinality":"one","engine":{"language":"x","languageVersion":"1.0.0","executionProfile":"p"},"programRef":"program.bin","programDigest":"` + valid.Resources[0].Digest + `"}`},
		{"invalid media type is resource validation", string(valid.PayloadBytes)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "invalid media type is resource validation" {
				invalid := valid
				invalid.Resources[0].MediaType = "not a media type;"
				_, err := Validate(invalid, validContext())
				assert.Error(t, err)
				return
			}
			_, err := Parse([]byte(test.data))
			assert.Error(t, err)
		})
	}
}

func TestValidateRejectsProgramBindingsAndSchemaMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*artifact.Artifact, *ValidationContext)
	}{
		{"missing program", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.Resources = nil }},
		{"referenced non-program resource", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.Resources[0].Role = "metadata" }},
		{"duplicate program", func(mapping *artifact.Artifact, _ *ValidationContext) {
			mapping.Resources = append(mapping.Resources, mapping.Resources[0])
		}},
		{"program name differs", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.Resources[0].Name = "other.bin" }},
		{"program bytes differ", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.Resources[0].Bytes = []byte("changed") }},
		{"source dependency differs", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.Dependencies[0].Revision = "other" }},
		{"target target-version differs", func(mapping *artifact.Artifact, _ *ValidationContext) { mapping.TargetSchema = "9.0.0" }},
		{"target is source record", func(_ *artifact.Artifact, context *ValidationContext) {
			context.Lineage[0].Schemas[1].Class = artifact.SchemaClassSource
		}},
		{"source descendant version", func(mapping *artifact.Artifact, _ *ValidationContext) {
			mapping.PayloadBytes = descriptorBytes(mapping.Type, mapping.Resources[0], `"version":"3.0.0"`, `"version":"2.0.0"`)
			mapping.Payload = descriptor(mapping.PayloadBytes, DescriptorMediaType, mapping.Payload.BundlePath)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapping, context := validMapping(t, []byte("opaque"))
			test.mutate(&mapping, &context)
			_, err := Validate(mapping, context)
			assert.Error(t, err)
		})
	}
}

func TestValidateAcceptsEmptyProgramAndAncestorTarget(t *testing.T) {
	mapping, context := validMapping(t, nil)
	mapping.TargetSchema = "1.0.0"
	mapping.PayloadBytes = descriptorBytes(mapping.Type, mapping.Resources[0], `"revision":"source-r1"`, `"revision":"target-r1"`)
	mapping.Payload = descriptor(mapping.PayloadBytes, DescriptorMediaType, mapping.Payload.BundlePath)
	context.Lineage[0].Schemas[1].Revision = "target-r1"
	mapping.Dependencies[1].Revision = "target-r1"
	context.Lineage = append(context.Lineage, Release{Version: "1.0.0", Schemas: []Schema{{Type: "target", Revision: "target-r1", Class: artifact.SchemaClassEntity}}})
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.Transformations[0].Program.Size)
}

func TestValidateRejectsRevisionReuseChange(t *testing.T) {
	mapping, context := validMapping(t, []byte("opaque"))
	descriptor, err := Parse(mapping.PayloadBytes)
	require.NoError(t, err)
	context.ReusedMappings = []Mapping{{Artifact: mapping, Descriptor: descriptor}}
	_, err = Validate(mapping, context)
	require.NoError(t, err)
	mapping.Dependencies[1].Revision = "another-target"
	_, err = Validate(mapping, context)
	assert.Error(t, err)
}

func TestValidateReleaseAcceptsTwoMappingsForOneSource(t *testing.T) {
	first, context := validMapping(t, []byte("one"))
	second, _ := validMapping(t, []byte("two"))
	second.Type = "other-mapping"
	second.Revision = "mapping-r2"
	second.PayloadBytes = descriptorBytes(second.Type, second.Resources[0], `"revision":"source-r1"`, `"version":"2.0.0"`)
	second.Payload = descriptor(second.PayloadBytes, DescriptorMediaType, "mappings/second.json")
	results, err := ValidateRelease([]artifact.Artifact{first, second}, context)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	_, err = ValidateRelease([]artifact.Artifact{first, first}, context)
	assert.Error(t, err)
}

func TestValidateIsRaceSafe(t *testing.T) {
	mapping, context := validMapping(t, []byte{0, 1, 2, 3})
	const workers = 32
	var group sync.WaitGroup
	errors := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := Validate(mapping, context)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		assert.NoError(t, err)
	}
}

func validMapping(t *testing.T, program []byte) (artifact.Artifact, ValidationContext) {
	t.Helper()
	resource := resource("program.bin", program)
	mapping := artifact.Artifact{
		Kind: artifact.KindSourceMapping, Type: "source-mapping", Revision: "mapping-r1", TargetSchema: "2.0.0",
		PayloadBytes: descriptorBytes("source-mapping", resource, `"revision":"source-r1"`, `"version":"2.0.0"`),
		Resources:    []artifact.Resource{resource},
		Dependencies: []artifact.Dependency{
			{Kind: artifact.KindSchema, Type: "source", Revision: "source-r1"},
			{Kind: artifact.KindSchema, Type: "target", Revision: "target-r2"},
		},
	}
	mapping.Payload = descriptor(mapping.PayloadBytes, DescriptorMediaType, "mappings/source.json")
	return mapping, validContext()
}

func validContext() ValidationContext {
	return ValidationContext{Version: "2.0.0", Lineage: []Release{{Version: "2.0.0", Schemas: []Schema{
		{Type: "source", Revision: "source-r1", Class: artifact.SchemaClassSource},
		{Type: "target", Revision: "target-r2", Class: artifact.SchemaClassEntity},
	}}}}
}

func descriptorBytes(id string, program artifact.Resource, sourceSelector, targetSelector string) []byte {
	return []byte(fmt.Sprintf(`{"$schema":"https://example.test/source-mapping-1.0.0","formatVersion":"1.0.0","id":%q,"sourceSchema":{"id":"source",%s},"engine":{"language":"publisher language with spaces","languageVersion":"1.2.3","executionProfile":"non-registry profile"},"programs":{"transformations":[{"targetSchema":{"id":"target",%s},"cardinality":"one","programRef":%q,"programDigest":%q}]}}`, id, sourceSelector, targetSelector, program.Name, program.Digest))
}

func rewriteDescriptor(t *testing.T, mapping *artifact.Artifact, mutate func(map[string]any)) {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(mapping.PayloadBytes, &document))
	mutate(document)
	data, err := json.Marshal(document)
	require.NoError(t, err)
	mapping.PayloadBytes = data
	mapping.Payload = descriptor(data, DescriptorMediaType, mapping.Payload.BundlePath)
}

func TestProgramStructureBoundaries(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte("[]"), []byte("{}")} {
		_, err := Parse(raw)
		require.Error(t, err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing programs", func(d map[string]any) { delete(d, "programs") }},
		{"null programs", func(d map[string]any) { d["programs"] = nil }},
		{"null condition", func(d map[string]any) { d["programs"].(map[string]any)["condition"] = nil }},
		{"missing transformations", func(d map[string]any) { delete(d["programs"].(map[string]any), "transformations") }},
		{"null transformations", func(d map[string]any) { d["programs"].(map[string]any)["transformations"] = nil }},
		{"empty transformations", func(d map[string]any) { d["programs"].(map[string]any)["transformations"] = []any{} }},
		{"null transformation", func(d map[string]any) { d["programs"].(map[string]any)["transformations"] = []any{nil} }},
		{"unknown programs field", func(d map[string]any) { d["programs"].(map[string]any)["unknown"] = true }},
		{"legacy target", func(d map[string]any) { d["targetSchema"] = map[string]any{"id": "target", "version": "2.0.0"} }},
		{"legacy cardinality", func(d map[string]any) { d["cardinality"] = "one" }},
		{"legacy program", func(d map[string]any) { d["programRef"] = "program.bin" }},
		{"legacy digest", func(d map[string]any) { d["programDigest"] = "digest" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapping, context := validMapping(t, nil)
			rewriteDescriptor(t, &mapping, test.mutate)
			_, err := Validate(mapping, context)
			require.Error(t, err)
		})
	}
}

func TestTransformationFieldsAndSelectors(t *testing.T) {
	for _, field := range []string{"programRef", "programDigest", "cardinality", "targetSchema"} {
		for _, invalid := range []any{nil, "", 42} {
			t.Run(fmt.Sprintf("%s-%v", field, invalid), func(t *testing.T) {
				mapping, context := validMapping(t, nil)
				rewriteDescriptor(t, &mapping, func(d map[string]any) {
					d["programs"].(map[string]any)["transformations"].([]any)[0].(map[string]any)[field] = invalid
				})
				_, err := Validate(mapping, context)
				require.Error(t, err)
			})
		}
	}
	for _, mutate := range []func(map[string]any){
		func(entry map[string]any) { entry["outputKind"] = "entity" },
		func(entry map[string]any) { entry["cardinality"] = "zero" },
		func(entry map[string]any) { entry["targetSchema"] = map[string]any{"id": "target"} },
		func(entry map[string]any) {
			entry["targetSchema"] = map[string]any{"id": "target", "version": "2.0.0", "revision": "target-r2"}
		},
	} {
		mapping, context := validMapping(t, nil)
		rewriteDescriptor(t, &mapping, func(d map[string]any) {
			mutate(d["programs"].(map[string]any)["transformations"].([]any)[0].(map[string]any))
		})
		_, err := Validate(mapping, context)
		require.Error(t, err)
	}
}

func TestSharedProgramConditionAndMixedTargets(t *testing.T) {
	mapping, context := validMapping(t, []byte{0, 255})
	context.Lineage[0].Schemas = append(context.Lineage[0].Schemas, Schema{Type: "relationship", Revision: "edge-r1", Class: artifact.SchemaClassEdge})
	mapping.Dependencies = append(mapping.Dependencies, artifact.Dependency{Kind: artifact.KindSchema, Type: "relationship", Revision: "edge-r1"})
	rewriteDescriptor(t, &mapping, func(d map[string]any) {
		programs := d["programs"].(map[string]any)
		first := programs["transformations"].([]any)[0].(map[string]any)
		programs["condition"] = map[string]any{"programRef": first["programRef"], "programDigest": first["programDigest"]}
		programs["transformations"] = append(programs["transformations"].([]any), map[string]any{
			"programRef": first["programRef"], "programDigest": first["programDigest"], "cardinality": "many",
			"targetSchema": map[string]any{"id": "relationship", "revision": "edge-r1"},
		}, first)
	})
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	require.NotNil(t, result.Condition)
	require.Len(t, result.Transformations, 3)
	assert.Equal(t, artifact.SchemaClassEdge, result.Transformations[1].OutputClass)
	assert.Equal(t, "many", result.Transformations[1].Cardinality)
	assert.Equal(t, "target", result.Transformations[2].Target.Type)
	assert.Equal(t, result.Condition.Name, result.Transformations[1].Program.Name)

	rewriteDescriptor(t, &mapping, func(d map[string]any) {
		d["programs"].(map[string]any)["condition"].(map[string]any)["cardinality"] = "one"
	})
	_, err = Validate(mapping, context)
	require.Error(t, err)
}

func TestProgramBindingFailuresAndImmutableReuse(t *testing.T) {
	for _, mutate := range []func(*artifact.Artifact){
		func(m *artifact.Artifact) { m.Resources = append(m.Resources, resource("unreferenced.bin", nil)) },
		func(m *artifact.Artifact) { m.Resources[0].Role = "condition" },
		func(m *artifact.Artifact) { m.Resources[0].Size++ },
		func(m *artifact.Artifact) { m.Resources[0].Digest = "sha-256:" + string(bytes.Repeat([]byte("0"), 64)) },
	} {
		mapping, context := validMapping(t, nil)
		mutate(&mapping)
		_, err := Validate(mapping, context)
		require.Error(t, err)
	}
	mapping, context := validMapping(t, []byte("original"))
	parsed, err := Parse(mapping.PayloadBytes)
	require.NoError(t, err)
	context.ReusedMappings = []Mapping{{Artifact: mapping, Descriptor: parsed}}
	mapping.Resources = []artifact.Resource{resource("program.bin", []byte("replacement"))}
	mapping.PayloadBytes = descriptorBytes(mapping.Type, mapping.Resources[0], `"revision":"source-r1"`, `"version":"2.0.0"`)
	mapping.Payload = descriptor(mapping.PayloadBytes, DescriptorMediaType, "mappings/source.json")
	_, err = Validate(mapping, context)
	require.Error(t, err)
}

func TestTransformationAndDescriptorCapacity(t *testing.T) {
	mapping, context := validMapping(t, nil)
	rewriteDescriptor(t, &mapping, func(d map[string]any) {
		programs := d["programs"].(map[string]any)
		first := programs["transformations"].([]any)[0]
		entries := make([]any, 10000)
		for index := range entries {
			entries[index] = first
		}
		programs["transformations"] = entries
	})
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	require.Len(t, result.Transformations, 10000)
	rewriteDescriptor(t, &mapping, func(d map[string]any) {
		programs := d["programs"].(map[string]any)
		entries := programs["transformations"].([]any)
		programs["transformations"] = append(entries, entries[0])
	})
	_, err = Parse(mapping.PayloadBytes)
	require.Error(t, err)

	mapping, _ = validMapping(t, nil)
	exact := append(bytes.Repeat([]byte(" "), config.MaxArtifactBytes-len(mapping.PayloadBytes)), mapping.PayloadBytes...)
	_, err = Parse(exact)
	require.NoError(t, err)
	_, err = Parse(append(exact, ' '))
	require.Error(t, err)
}

func resource(name string, data []byte) artifact.Resource {
	return artifact.Resource{Name: name, Role: "program", Descriptor: descriptor(data, "application/vnd.example.opaque-program", "mappings/"+name), Bytes: data}
}

func descriptor(data []byte, mediaType, bundlePath string) artifact.Descriptor {
	digest := sha256.Sum256(data)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: bundlePath}
}
