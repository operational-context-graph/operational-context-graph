// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// origin supplies the fixed Registry origin used by artifact-validation fixtures.
const origin = "https://registry.example.test"

// TestArtifactMetadata checks byte exclusion, metadata preservation, and independent resource storage.
func TestArtifactMetadata(t *testing.T) {
	payload, program := []byte(`true`), []byte{0, 255}
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnderSchemaVersion: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1",
		Payload:      descriptor(payload, SchemaMediaType, "schemas/device.json"), PayloadBytes: payload,
		Resources: []Resource{
			{Name: "program.bin", Role: ResourceRoleProgram, Descriptor: descriptor(program, "application/octet-stream", "resources/program.bin"), Bytes: program},
		},
		Dependencies: []Dependency{{Kind: KindContext, Type: "device-context", Revision: "ctx-1"}},
		Extensions:   map[string]any{"org.example.note": "retained"},
	}
	expected := item
	expected.PayloadBytes = nil
	expected.Resources = append([]Resource(nil), item.Resources...)
	expected.Resources[0].Bytes = nil
	metadata := item.Metadata()
	assert.Equal(t, expected, metadata)
	assert.Equal(t, payload, item.PayloadBytes)
	assert.Equal(t, program, item.Resources[0].Bytes)
	metadata.Resources[0].BundlePath = "repacked/program.bin"
	assert.Equal(t, "resources/program.bin", item.Resources[0].BundlePath)

	for _, resources := range [][]Resource{nil, {}} {
		metadata := (Artifact{Resources: resources}).Metadata()
		assert.Equal(t, []Resource{}, metadata.Resources)
	}
}

// TestArtifactValidation checks a valid boolean schema and rejection of an unsafe revision identifier.
func TestArtifactValidation(t *testing.T) {
	payload := []byte(`true`)
	item := Artifact{
		Kind:                      KindSchema,
		Type:                      "device",
		Revision:                  "rev-1",
		CreatedUnderSchemaVersion: "1.0.0",
		SchemaClass:               SchemaClassEntity,
		CanonicalURI:              origin + "/v1/resources/schema/device/rev-1",
		Payload:                   descriptor(payload, "application/schema+json", "bundles/device.json"),
		PayloadBytes:              payload,
	}
	require.NoError(t, item.Validate(origin))
	require.NoError(t, item.ValidatePayloadFormat())

	item.Revision = ".."
	assert.Error(t, item.Validate(origin))
}

// TestResourceRoleRemainsExtensible preserves well-formed ancillary roles while rejecting empty or malformed role identifiers.
func TestResourceRoleRemainsExtensible(t *testing.T) {
	for _, role := range []ResourceRole{ResourceRoleProgram, "attachment", "publisher-metadata", "", "Invalid", "not a role"} {
		t.Run(string(role), func(t *testing.T) {
			payload := []byte(`true`)
			item := Artifact{
				Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnderSchemaVersion: "1.0.0", SchemaClass: SchemaClassEntity,
				CanonicalURI: origin + "/v1/resources/schema/device/rev-1",
				Payload:      descriptor(payload, SchemaMediaType, "schemas/device.json"), PayloadBytes: payload,
				Resources: []Resource{
					{
						Name:       "metadata.bin",
						Role:       role,
						Descriptor: descriptor(nil, "application/octet-stream", "resources/metadata.bin"),
					},
				},
			}
			err := item.Validate(origin)
			if role == ResourceRoleProgram || role == "attachment" || role == "publisher-metadata" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestDescriptorValidationRejectsMismatches checks exact byte counts and unsafe descriptor-related identifiers.
func TestDescriptorValidationRejectsMismatches(t *testing.T) {
	data := []byte("payload")
	valid := descriptor(data, "application/octet-stream", "resources/payload")
	require.NoError(t, ValidateDescriptor(valid, data))
	valid.Size++
	assert.Error(t, ValidateDescriptor(valid, data))
	assert.False(t, ValidBundlePath("../payload"))
	assert.False(t, ValidResourceName("a/b"))
	assert.False(t, ValidLogicalType("Uppercase"))
}

// TestValidateCanonicalURI verifies Registry-origin ownership and exact immutable path coordinates.
func TestValidateCanonicalURI(t *testing.T) {
	require.NoError(t, ValidateCanonicalURI(origin+"/v1/resources/schema/device/rev-1", origin, KindSchema, "device", "rev-1"))
	assert.Error(t, ValidateCanonicalURI("https://other.example.test/v1/resources/schema/device/rev-1", origin, KindSchema, "device", "rev-1"))
	assert.Error(t, ValidateCanonicalURI(origin+"/v1/resources/schema/device/current", origin, KindSchema, "device", "rev-1"))
}

// TestValidDocumentURI preserves syntax rules and rejects malformed or empty identifiers without fetching them.
func TestValidDocumentURI(t *testing.T) {
	for _, value := range []string{
		"https://example.test/schema", "urn:example:schema", "https://example.test/é",
	} {
		assert.True(t, ValidDocumentURI(value), "URI %q", value)
	}
	for _, value := range []string{
		"", "/relative", "https://example.test/schema?query=value", "https://example.test/schema#fragment",
		"https://example.test/%zz", "https://example.test/\n", "https://example.test/" + string([]byte{0xff}),
	} {
		assert.False(t, ValidDocumentURI(value), "URI %q", value)
	}
}

// TestValidDocumentURIByteBoundaries checks exact and excessive UTF-8 byte lengths independently of character count.
func TestValidDocumentURIByteBoundaries(t *testing.T) {
	for _, unit := range []string{"a", "é"} {
		for _, size := range []int{schemaartifacts.MaxURIBytes - 1, schemaartifacts.MaxURIBytes, schemaartifacts.MaxURIBytes + 1} {
			t.Run(fmt.Sprintf("%s/%d", unit, size), func(t *testing.T) {
				const prefix = "https://example.test/"
				remaining := size - len(prefix)
				value := prefix + strings.Repeat(unit, remaining/len(unit)) + strings.Repeat("a", remaining%len(unit))
				require.Len(t, value, size)
				assert.Equal(t, size <= schemaartifacts.MaxURIBytes, ValidDocumentURI(value))
			})
		}
	}
}

// TestPayloadFormatFailsClosedForExternalReferences rejects schema references unavailable from the caller's catalog.
func TestPayloadFormatFailsClosedForExternalReferences(t *testing.T) {
	payload := []byte(`{"$ref":"https://example.test/unavailable"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnderSchemaVersion: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, SchemaMediaType, "bundles/device.json"), PayloadBytes: payload,
	}
	assert.Error(t, item.ValidatePayloadFormat())
}

// TestPayloadFormatAcceptsCatalogOnlySchemaReference resolves a schema reference entirely from caller-supplied documents.
func TestPayloadFormatAcceptsCatalogOnlySchemaReference(t *testing.T) {
	referenced := map[string]any{"type": "string"}
	payload := []byte(`{"$ref":"https://example.test/catalog"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnderSchemaVersion: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, SchemaMediaType, "bundles/device.json"), PayloadBytes: payload,
	}
	require.NoError(t, item.ValidatePayloadFormatWithCatalog(map[string]any{"https://example.test/catalog": referenced}))
}

// descriptor builds test metadata whose size and digest match the supplied exact bytes.
func descriptor(data []byte, mediaType, bundlePath string) Descriptor {
	digest := sha256.Sum256(data)
	return Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: bundlePath}
}

// TestMultiProgramResourcesAndDistinctDependencies checks shared resource roles, unique identities, byte integrity, and descriptor bounds.
func TestMultiProgramResourcesAndDistinctDependencies(t *testing.T) {
	item := Artifact{
		Kind:                      KindSourceMapping,
		Type:                      "mapping",
		Revision:                  "mapping-r1",
		CreatedUnderSchemaVersion: "1.0.0",
		TargetSchema:              "1.0.0",
		CanonicalURI:              origin + "/v1/resources/source-mapping/mapping/mapping-r1",
		PayloadBytes:              []byte(`{}`),
		Payload:                   descriptor([]byte(`{}`), "application/json", "mapping/descriptor.json"),
		Resources: []Resource{
			{Name: "condition.bin", Role: "program", Descriptor: descriptor(nil, "application/octet-stream", "mapping/condition.bin")},
			{Name: "entity.bin", Role: "program", Descriptor: descriptor([]byte{0, 255}, "application/octet-stream", "mapping/entity.bin"), Bytes: []byte{0, 255}},
			{Name: "edge.bin", Role: "program", Descriptor: descriptor(nil, "application/octet-stream", "mapping/edge.bin")},
		},
		Dependencies: []Dependency{
			{Kind: KindSchema, Type: "source", Revision: "source-r1"},
			{Kind: KindSchema, Type: "entity", Revision: "entity-r1"},
			{Kind: KindSchema, Type: "edge", Revision: "edge-r1"},
		},
	}
	require.NoError(t, item.Validate(origin))
	for _, mutate := range []func(*Artifact){
		func(a *Artifact) { a.Resources = append(a.Resources, a.Resources[0]) },
		func(a *Artifact) { a.Dependencies = append(a.Dependencies, a.Dependencies[0]) },
		func(a *Artifact) { a.Resources[1].Bytes = []byte("changed") },
		func(a *Artifact) { a.Resources[0].Name = "../condition" },
		func(a *Artifact) { a.CanonicalURI = origin + "/v1/resources/source-mapping/mapping/current" },
		func(a *Artifact) { a.Type = strings.Repeat("a", 256) },
	} {
		itemCopy := item
		itemCopy.Resources = append([]Resource(nil), item.Resources...)
		itemCopy.Dependencies = append([]Dependency(nil), item.Dependencies...)
		mutate(&itemCopy)
		require.Error(t, itemCopy.Validate(origin))
	}
}

// TestProgramByteDescriptorBoundaries verifies empty content and the exact maximum accepted resource byte count.
func TestProgramByteDescriptorBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, schemaartifacts.MaxArtifactBytes, schemaartifacts.MaxArtifactBytes + 1} {
		data := bytes.Repeat([]byte{255}, size)
		err := ValidateDescriptor(descriptor(data, "application/octet-stream", "mapping/program.bin"), data)
		if size > schemaartifacts.MaxArtifactBytes {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
}
