// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package sourcemapping

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAcceptsOpaqueProgramAndDerivesOutputClass(t *testing.T) {
	mapping, context := validMapping(t, []byte{0, 255, 1})
	result, err := Validate(mapping, context)
	require.NoError(t, err)
	assert.Equal(t, artifact.SchemaClassEntity, result.OutputClass)
	assert.Equal(t, mapping.Resources[0], result.Program)
	assert.Equal(t, "publisher language with spaces", result.Descriptor.Engine.Language)
	assert.Equal(t, "non-registry profile", result.Descriptor.Engine.ExecutionProfile)
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
		{"additional non-program resource", func(mapping *artifact.Artifact, _ *ValidationContext) {
			extra := resource("metadata.bin", []byte("metadata"))
			extra.Role = "metadata"
			mapping.Resources = append(mapping.Resources, extra)
		}},
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
	assert.Equal(t, int64(0), result.Program.Size)
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

func TestValidateReleaseRejectsTwoMappingsForOneSource(t *testing.T) {
	first, context := validMapping(t, []byte("one"))
	second, _ := validMapping(t, []byte("two"))
	second.Type = "other-mapping"
	second.Revision = "mapping-r2"
	second.PayloadBytes = descriptorBytes(second.Type, second.Resources[0], `"revision":"source-r1"`, `"version":"2.0.0"`)
	_, err := ValidateRelease([]artifact.Artifact{first, second}, context)
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
	return []byte(fmt.Sprintf(`{"$schema":"https://example.test/source-mapping-1.0.0","formatVersion":"1.0.0","id":%q,"sourceSchema":{"id":"source",%s},"targetSchema":{"id":"target",%s},"cardinality":"one","engine":{"language":"publisher language with spaces","languageVersion":"1.2.3","executionProfile":"non-registry profile"},"programRef":%q,"programDigest":%q}`, id, sourceSelector, targetSelector, program.Name, program.Digest))
}

func resource(name string, data []byte) artifact.Resource {
	return artifact.Resource{Name: name, Role: "program", Descriptor: descriptor(data, "application/vnd.example.opaque-program", "mappings/"+name), Bytes: data}
}

func descriptor(data []byte, mediaType, bundlePath string) artifact.Descriptor {
	digest := sha256.Sum256(data)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: bundlePath}
}
