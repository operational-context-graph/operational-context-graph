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

// TestSchemaDraft2020Acceptance checks acceptance of Draft 2020-12 and rejection of older dialects.
func TestSchemaDraft2020Acceptance(t *testing.T) {
	for _, dialect := range []string{"http://json-schema.org/draft-04/schema", "http://json-schema.org/draft-07/schema", "https://json-schema.org/draft/2019-09/schema", "https://json-schema.org/draft/2020-12/schema"} {
		t.Run(dialect, func(t *testing.T) {
			a := Artifact{Kind: KindSchema, CanonicalURI: origin + "/v1/resources/schema/device/r1", Payload: Descriptor{MediaType: SchemaMediaType}, PayloadBytes: []byte(fmt.Sprintf(`{"$schema":%q,"type":"object"}`, dialect))}
			err := a.ValidatePayloadFormat()
			if dialect == "https://json-schema.org/draft/2020-12/schema" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "Draft 2020-12")
			}
		})
	}
}

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

// TestArtifactValidation checks identity, kind-specific metadata, dependencies, and payload integrity.
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

	for _, test := range []struct {
		name   string
		mutate func(*Artifact)
		want   string
	}{
		{"kind", func(a *Artifact) { a.Kind = "unknown" }, "invalid artifact identity"},
		{"type", func(a *Artifact) { a.Type = "Device" }, "invalid artifact identity"},
		{"revision", func(a *Artifact) { a.Revision = ".." }, "invalid artifact identity"},
		{"creation version", func(a *Artifact) { a.CreatedUnderSchemaVersion = "1.0" }, "invalid artifact identity"},
		{"schema class", func(a *Artifact) { a.SchemaClass = "unknown" }, "schema requires a class"},
		{"schema target", func(a *Artifact) { a.TargetSchema = "1.0.0" }, "schema requires a class"},
		{"context class", func(a *Artifact) { a.Kind = KindContext }, "context cannot declare"},
		{"context target", func(a *Artifact) { a.Kind, a.SchemaClass, a.TargetSchema = KindContext, "", "1.0.0" }, "context cannot declare"},
		{"mapping class", func(a *Artifact) { a.Kind, a.TargetSchema = KindSourceMapping, "1.0.0" }, "source mapping requires"},
		{"mapping target", func(a *Artifact) { a.Kind, a.SchemaClass = KindSourceMapping, "" }, "source mapping requires"},
		{"payload digest", func(a *Artifact) { a.Payload.Digest = descriptor([]byte(`false`), SchemaMediaType, "x").Digest }, "validate payload: descriptor digest"},
		{"dependency kind", func(a *Artifact) { a.Dependencies = []Dependency{{Kind: "unknown", Type: "x", Revision: "r1"}} }, "invalid artifact dependency"},
		{"dependency type", func(a *Artifact) { a.Dependencies = []Dependency{{Kind: KindSchema, Type: "X", Revision: "r1"}} }, "invalid artifact dependency"},
		{"dependency revision", func(a *Artifact) { a.Dependencies = []Dependency{{Kind: KindSchema, Type: "x", Revision: ".."}} }, "invalid artifact dependency"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := item
			test.mutate(&changed)
			require.ErrorContains(t, changed.Validate(origin), test.want)
		})
	}
	for _, class := range []SchemaClass{SchemaClassEntity, SchemaClassIdentifier, SchemaClassEdge, SchemaClassSource} {
		item.SchemaClass = class
		require.NoError(t, item.Validate(origin))
	}
	item.Kind, item.SchemaClass = KindContext, ""
	item.CanonicalURI = origin + "/v1/resources/context/device/rev-1"
	require.NoError(t, item.Validate(origin))
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

// TestDescriptorValidationRejectsMismatches isolates metadata errors from size and digest mismatches.
func TestDescriptorValidationRejectsMismatches(t *testing.T) {
	data := []byte("payload")
	valid := descriptor(data, "application/octet-stream", "resources/payload")
	require.NoError(t, ValidateDescriptor(valid, data))
	for _, test := range []struct {
		name   string
		mutate func(*Descriptor)
		want   string
	}{
		{"empty media type", func(d *Descriptor) { d.MediaType = "" }, "descriptor requires"},
		{"long media type", func(d *Descriptor) { d.MediaType = strings.Repeat("a", schemaartifacts.MaxIdentifierBytes+1) }, "descriptor requires"},
		{"malformed media type", func(d *Descriptor) { d.MediaType = "not a media type;" }, "invalid media type"},
		{"empty path", func(d *Descriptor) { d.BundlePath = "" }, "descriptor requires"},
		{"unsafe path", func(d *Descriptor) { d.BundlePath = "../payload" }, "invalid byte descriptor"},
		{"invalid digest", func(d *Descriptor) { d.Digest = "invalid" }, "invalid byte descriptor"},
		{"negative size", func(d *Descriptor) { d.Size = -1 }, "invalid byte descriptor"},
		{"excess size", func(d *Descriptor) { d.Size = schemaartifacts.MaxArtifactBytes + 1 }, "invalid byte descriptor"},
		{"size mismatch", func(d *Descriptor) { d.Size++ }, "descriptor size does not match"},
		{"digest mismatch", func(d *Descriptor) { d.Digest = descriptor([]byte("PAYLOAD"), d.MediaType, d.BundlePath).Digest }, "descriptor digest does not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := valid
			test.mutate(&changed)
			require.ErrorContains(t, ValidateDescriptor(changed, data), test.want)
		})
	}
}

// TestValidateCanonicalURI verifies Registry-origin ownership and exact immutable path coordinates.
func TestValidateCanonicalURI(t *testing.T) {
	const path = "/v1/resources/schema/device/rev-1"
	for _, test := range []struct {
		name, uri, origin, want string
	}{
		{"valid", origin + path, origin, ""},
		{"empty", "", origin, "length"},
		{"too long", origin + path + strings.Repeat("a", schemaartifacts.MaxURIBytes), origin, "length"},
		{"relative", path, origin, "absolute"},
		{"malformed", "https://example.test/%zz", origin, "absolute"},
		{"other host", "https://other.example.test" + path, origin, "Registry origin"},
		{"other scheme", "http://registry.example.test" + path, origin, "Registry origin"},
		{"other port", origin + ":443" + path, origin, "Registry origin"},
		{"relative origin", origin + path, "/registry", "Registry origin"},
		{"malformed origin", origin + path, "https://%zz", "Registry origin"},
		{"wrong revision", origin + "/v1/resources/schema/device/current", origin, "invalid path"},
		{"escaped path", origin + "/v1/resources/schema/%64evice/rev-1", origin, "invalid path"},
		{"query", origin + path + "?x=1", origin, "invalid path"},
		{"fragment", origin + path + "#x", origin, "invalid path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCanonicalURI(test.uri, test.origin, KindSchema, "device", "rev-1")
			if test.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.want)
			}
		})
	}
}

// TestLexicalValidators checks accepted spellings and rejected traversal, encoding, and length boundaries.
func TestLexicalValidators(t *testing.T) {
	for _, test := range []struct {
		name     string
		validate func(string) bool
		valid    []string
		invalid  []string
	}{
		{"logical type", ValidLogicalType, []string{"0", "device-2", strings.Repeat("a", schemaartifacts.MaxIdentifierBytes)}, []string{"", "Device", "a_b", "-a", "a-", "a--b", "é", strings.Repeat("a", schemaartifacts.MaxIdentifierBytes+1)}},
		{"revision", ValidRevision, []string{"Rev-1._~", "...", strings.Repeat("a", schemaartifacts.MaxIdentifierBytes)}, []string{"", ".", "..", "a/b", "a%20b", "é", strings.Repeat("a", schemaartifacts.MaxIdentifierBytes+1)}},
		{"resource", ValidResourceName, []string{"program.bin", "A_~"}, []string{"", ".", "..", "a/b", `a\b`}},
		{"version", ValidVersion, []string{"0.0.0", "10.20.30"}, []string{"", "1.0", "01.0.0", "1.0.0-beta", "1.0.0+build", "-1.0.0"}},
		{"digest", ValidDigest, []string{"sha-256:" + strings.Repeat("a", 64)}, []string{"", "sha256:" + strings.Repeat("a", 64), "sha-256:" + strings.Repeat("A", 64), "sha-256:" + strings.Repeat("a", 63), "sha-256:" + strings.Repeat("a", 65)}},
		{"bundle path", ValidBundlePath, []string{"payload", "a/b.json"}, []string{"", "/payload", `a\b`, "a//b", "a/./b", "a/../b", "a/"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.valid {
				assert.True(t, test.validate(value), "%q", value)
			}
			for _, value := range test.invalid {
				assert.False(t, test.validate(value), "%q", value)
			}
		})
	}
}

// TestPayloadFormats checks schema compilation, JSON-LD contexts, and strict parsing for every artifact kind.
func TestPayloadFormats(t *testing.T) {
	for _, test := range []struct {
		name, payload, mediaType string
		kind                     Kind
		catalog                  map[string]any
		want                     string
	}{
		{"boolean schema", `false`, SchemaMediaType, KindSchema, nil, ""},
		{"local schema reference", `{"$defs":{"value":{"type":"string"}},"$ref":"#/$defs/value"}`, SchemaMediaType, KindSchema, nil, ""},
		{"invalid schema", `{"type":"unknown"}`, SchemaMediaType, KindSchema, nil, "compile JSON Schema"},
		{"schema media type", `true`, "application/json", KindSchema, nil, "schema payload must use"},
		{"inline context", `{"@context":{"name":"https://example.test/name"}}`, ContextMediaType, KindContext, nil, ""},
		{"empty context", `{"@context":{}}`, ContextMediaType, KindContext, nil, ""},
		{"context media type", `{"@context":{}}`, "application/ld+json", KindContext, nil, "context payload must use"},
		{"missing context", `{}`, ContextMediaType, KindContext, nil, "must contain @context"},
		{"null context", `{"@context":null}`, ContextMediaType, KindContext, nil, "must contain @context"},
		{"non-object context", `[]`, ContextMediaType, KindContext, nil, "must contain @context"},
		{"invalid context", `{"@context":42}`, ContextMediaType, KindContext, nil, "validate JSON-LD context"},
		{"uncataloged context", `{"@context":"https://example.test/context"}`, ContextMediaType, KindContext, nil, "validate JSON-LD context"},
		{"catalog context", `{"@context":"https://example.test/context"}`, ContextMediaType, KindContext, map[string]any{"https://example.test/context": map[string]any{"@context": map[string]any{"name": "https://example.test/name"}}}, ""},
		{"mapping JSON only", `{}`, "application/json", KindSourceMapping, nil, ""},
		{"malformed payload", `{"a":`, SchemaMediaType, KindSchema, nil, "malformed JSON"},
		{"duplicate context", `{"@context":{},"@context":{}}`, ContextMediaType, KindContext, nil, "duplicate JSON object member"},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := Artifact{Kind: test.kind, CanonicalURI: origin + "/v1/resources/schema/device/rev-1", Payload: Descriptor{MediaType: test.mediaType}, PayloadBytes: []byte(test.payload)}
			err := item.ValidatePayloadFormatWithCatalog(test.catalog)
			if test.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.want)
			}
		})
	}
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
