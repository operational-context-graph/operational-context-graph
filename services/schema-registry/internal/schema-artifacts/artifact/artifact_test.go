// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const origin = "https://registry.example.test"

func TestArtifactValidation(t *testing.T) {
	payload := []byte(`true`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, "application/schema+json", "bundles/device.json"), PayloadBytes: payload,
	}
	require.NoError(t, item.Validate(origin))
	require.NoError(t, item.ValidatePayloadFormat())

	item.Revision = ".."
	assert.Error(t, item.Validate(origin))
}

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

func TestValidateCanonicalURI(t *testing.T) {
	require.NoError(t, ValidateCanonicalURI(origin+"/v1/resources/schema/device/rev-1", origin, KindSchema, "device", "rev-1"))
	assert.Error(t, ValidateCanonicalURI("https://other.example.test/v1/resources/schema/device/rev-1", origin, KindSchema, "device", "rev-1"))
	assert.Error(t, ValidateCanonicalURI(origin+"/v1/resources/schema/device/current", origin, KindSchema, "device", "rev-1"))
}

func TestPayloadFormatFailsClosedForExternalReferences(t *testing.T) {
	payload := []byte(`{"$ref":"https://example.test/unavailable"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, SchemaMediaType, "bundles/device.json"), PayloadBytes: payload,
	}
	assert.Error(t, item.ValidatePayloadFormat())
}

func TestPayloadFormatAcceptsCatalogOnlySchemaReference(t *testing.T) {
	referenced := map[string]any{"type": "string"}
	payload := []byte(`{"$ref":"https://example.test/catalog"}`)
	item := Artifact{
		Kind: KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: SchemaClassEntity,
		CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, SchemaMediaType, "bundles/device.json"), PayloadBytes: payload,
	}
	require.NoError(t, item.ValidatePayloadFormatWithCatalog(map[string]any{"https://example.test/catalog": referenced}))
}

func descriptor(data []byte, mediaType, bundlePath string) Descriptor {
	digest := sha256.Sum256(data)
	return Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: bundlePath}
}
