// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// origin supplies the fixed Registry origin used by artifact-validation fixtures.
const origin = "https://registry.example.test"

// TestArtifactValidation checks a valid boolean schema and rejection of an unsafe revision identifier.
func TestArtifactValidation(t *testing.T) {
	payload := []byte(`true`)
	item := Artifact{
		Kind:         KindSchema,
		Type:         "device",
		Revision:     "rev-1",
		CreatedUnder: "1.0.0",
		SchemaClass:  SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1",
		Payload:      descriptor(payload, "application/schema+json", "bundles/device.json"),
		PayloadBytes: payload,
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
				Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
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

// TestPayloadFormatFailsClosedForExternalReferences rejects schema references unavailable from the caller's catalog.
func TestPayloadFormatFailsClosedForExternalReferences(t *testing.T) {
	payload := []byte(`{"$ref":"https://example.test/unavailable"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, SchemaMediaType, "bundles/device.json"), PayloadBytes: payload,
	}
	assert.Error(t, item.ValidatePayloadFormat())
}

// TestPayloadFormatAcceptsCatalogOnlySchemaReference resolves a schema reference entirely from caller-supplied documents.
func TestPayloadFormatAcceptsCatalogOnlySchemaReference(t *testing.T) {
	referenced := map[string]any{"type": "string"}
	payload := []byte(`{"$ref":"https://example.test/catalog"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
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
		Kind:         KindSourceMapping,
		Type:         "mapping",
		Revision:     "mapping-r1",
		CreatedUnder: "1.0.0",
		TargetSchema: "1.0.0",
		CanonicalURI: origin + "/v1/resources/source-mapping/mapping/mapping-r1",
		PayloadBytes: []byte(`{}`),
		Payload:      descriptor([]byte(`{}`), "application/json", "mapping/descriptor.json"),
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
