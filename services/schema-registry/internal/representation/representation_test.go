// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package representation

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/stretchr/testify/require"
)

const testVersion = "1.2.3"

func testBytes(content []byte, mediaType string) model.ByteDescriptor {
	return model.ByteDescriptor{
		MediaType: mediaType,
		Digest:    fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
		Size:      int64(len(content)),
		Bytes:     content,
	}
}

func testPackage(program []byte) model.MappingPackage {
	sourceClass, targetClass, targetVersion := "source-record", "entity-type", testVersion
	source := model.Descriptor{Artifact: model.Artifact{
		Revision: "rev-source", Kind: "schema", Type: "incoming", CreatedUnderSchemaVersion: "1.0.0",
		SchemaClass: &sourceClass, CanonicalURI: "https://example.test/incoming",
		Payload:    testBytes([]byte("true"), "application/schema+json"),
		Extensions: []byte(`{"publisher":"source <&> 世界"}`),
	}}
	target := model.Descriptor{Artifact: model.Artifact{
		Revision: "rev-target", Kind: "schema", Type: "device", CreatedUnderSchemaVersion: "1.1.0",
		SchemaClass: &targetClass, CanonicalURI: "https://example.test/device",
		Payload:    testBytes([]byte(` { "type": "object", "title": "device <&> 世界", "minimum": 9007199254740993 } `), "application/schema+json"),
		Extensions: []byte(`{"publisher":{"name":"target","enabled":true}}`),
	}}
	condition := model.Resource{ArtifactRevision: "rev-mapping", Name: "condition.bin", Role: "program", ByteDescriptor: testBytes([]byte{0, 0xff, 0x80}, "application/octet-stream")}
	transformation := model.Resource{ArtifactRevision: "rev-mapping", Name: "transform.bin", Role: "program", ByteDescriptor: testBytes(program, "application/octet-stream")}
	ancillary := model.Resource{
		ArtifactRevision: "rev-mapping", Name: "ancillary.bin", Role: "attachment",
		ByteDescriptor: model.ByteDescriptor{MediaType: "application/octet-stream", Digest: "sha256:metadata-only", Size: 16 << 20},
	}
	mapping := model.Descriptor{
		Artifact: model.Artifact{
			Revision: "rev-mapping", Kind: "source-mapping", Type: "device-mapping", CreatedUnderSchemaVersion: "1.0.0",
			TargetSchemaVersion: &targetVersion, CanonicalURI: "https://example.test/device-mapping",
			Payload:    testBytes([]byte(`{"formatVersion":1}`), "application/json"),
			Extensions: []byte(`{"publisher":"mapping <&> 世界","number":9007199254740993}`),
		},
		Resources: []model.Resource{condition, transformation, ancillary},
		Dependencies: []model.Dependency{
			{ArtifactRevision: "rev-mapping", Kind: "schema", ArtifactType: "incoming", Revision: "rev-source"},
			{ArtifactRevision: "rev-mapping", Kind: "schema", ArtifactType: "device", Revision: "rev-target"},
		},
		SourceArtifactType: "incoming",
	}
	return model.MappingPackage{
		Mapping: mapping,
		Detail: model.SourceMappingDetail{
			SchemaVersion: testVersion, MappingRevision: "rev-mapping", SourceArtifactType: "incoming", SourceRevision: "rev-source",
			TargetSchemaVersion: testVersion, ExecutionProfile: "opaque-profile", EngineLanguage: "opaque-language", EngineLanguageVersion: "2026",
			Condition: &model.ProgramBinding{ProgramName: condition.Name, ProgramDigest: condition.Digest},
			Transformations: []model.MappingTransformation{
				{ProgramBinding: model.ProgramBinding{ProgramName: transformation.Name, ProgramDigest: transformation.Digest}, TargetArtifactType: "device", TargetRevision: "rev-target", Cardinality: "one"},
				{ProgramBinding: model.ProgramBinding{ProgramName: transformation.Name, ProgramDigest: transformation.Digest}, TargetArtifactType: "incoming", TargetRevision: "rev-source", Cardinality: "many"},
			},
		},
		Programs: []model.Resource{condition, transformation},
		Schemas:  []model.Descriptor{source, target},
		Stores: []model.StorageEntry{
			{SchemaVersion: testVersion, ArtifactType: "device", Store: "data-center-inventory"},
			{SchemaVersion: testVersion, ArtifactType: "device", Store: "insights-repository"},
		},
	}
}

func testJSON(t testing.TB, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func requireJSONKeys(t testing.TB, value any, keys ...string) {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(testJSON(t, value), &fields))
	actual := make([]string, 0, len(fields))
	for key := range fields {
		actual = append(actual, key)
	}
	require.ElementsMatch(t, keys, actual)
}

func TestBuildDescriptor(t *testing.T) {
	for _, kind := range []string{"schema", "source-mapping", "context"} {
		for _, version := range []string{"", testVersion} {
			t.Run(kind+"/"+version, func(t *testing.T) {
				value := testPackage(nil).Mapping
				value.Artifact.Kind = kind
				before := testJSON(t, value)
				got := BuildDescriptor(value, version)
				expected := DescriptorResponse{
					SchemaVersion: version, Kind: kind, ArtifactType: "device-mapping", ArtifactRevision: "rev-mapping",
					CreatedUnderSchemaVersion: "1.0.0", TargetSchemaVersion: value.Artifact.TargetSchemaVersion,
					CanonicalURI: "https://example.test/device-mapping",
					Payload:      PayloadResponse{MediaType: "application/json", Digest: value.Artifact.Payload.Digest, Size: 19, Href: "/v1/artifacts/rev-mapping"},
					Resources: []ResourceResponse{
						{Name: "condition.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Resources[0].Digest, Size: 3, Href: "/v1/artifacts/rev-mapping/resources/condition.bin"},
						{Name: "transform.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Resources[1].Digest, Size: 0, Href: "/v1/artifacts/rev-mapping/resources/transform.bin"},
						{Name: "ancillary.bin", Role: "attachment", MediaType: "application/octet-stream", Digest: "sha256:metadata-only", Size: 16 << 20, Href: "/v1/artifacts/rev-mapping/resources/ancillary.bin"},
					},
					Dependencies: []DependencyResponse{
						{Kind: "schema", ArtifactType: "incoming", ArtifactRevision: "rev-source", Href: "/v1/artifacts/rev-source/metadata"},
						{Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-target", Href: "/v1/artifacts/rev-target/metadata"},
					},
					Extensions: json.RawMessage(`{"publisher":"mapping <&> 世界","number":9007199254740993}`),
					Links: map[string]string{
						"self": "/v1/artifacts/rev-mapping/metadata", "content": "/v1/artifacts/rev-mapping",
						"history": "/v1/artifact-kinds/" + kind + "/device-mapping/history",
					},
				}
				if version != "" {
					expected.Links["self"] = "/v1/releases/1.2.3/artifacts/" + kind + "/device-mapping"
					expected.Dependencies[0].Href = "/v1/releases/1.2.3/artifacts/schema/incoming"
					expected.Dependencies[1].Href = "/v1/releases/1.2.3/artifacts/schema/device"
					if kind == "schema" {
						expected.Links["contexts"] = "/v1/releases/1.2.3/schemas/device-mapping/contexts"
						expected.Links["package"] = "/v1/releases/1.2.3/schemas/device-mapping/package"
						expected.Links["sourceMappings"] = "/v1/releases/1.2.3/source-mappings?sourceArtifactType=device-mapping"
						expected.Links["stores"] = "/v1/releases/1.2.3/schemas/device-mapping/stores"
					}
					if kind == "source-mapping" {
						expected.Links["self"] = "/v1/releases/1.2.3/source-mappings/device-mapping"
						expected.Links["package"] = "/v1/releases/1.2.3/source-mappings/device-mapping/package"
					}
				}
				require.Equal(t, expected, got)
				require.Equal(t, testJSON(t, expected), testJSON(t, got))
				require.Equal(t, before, testJSON(t, value), "descriptor building must not mutate its input")
			})
		}
	}
}

func TestBuildDescriptorEmptyCollectionsAndOptionalFields(t *testing.T) {
	got := BuildDescriptor(model.Descriptor{}, "")
	require.NotNil(t, got.Resources)
	require.Empty(t, got.Resources)
	require.NotNil(t, got.Dependencies)
	require.Empty(t, got.Dependencies)
	requireJSONKeys(t, got, "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "links")
	require.Contains(t, string(testJSON(t, got)), `"resources":[],"dependencies":[]`)

	schema := testPackage(nil).Schemas[0]
	require.Equal(t, "source-record", *BuildDescriptor(schema, testVersion).SchemaClass)
}

func TestBuildMappingAndPackagePreserveCompleteContract(t *testing.T) {
	value := testPackage([]byte{0, 0xff, 0x80, 0x7f})
	before := testJSON(t, value)
	mapping := BuildMapping(model.Mapping{Descriptor: value.Mapping, Detail: value.Detail}, testVersion)
	require.Equal(t, BuildDescriptor(value.Mapping, testVersion), mapping.DescriptorResponse)
	require.Equal(t, MappingSourceSchemaResponse{ArtifactRevision: "rev-source", ArtifactType: "incoming", Content: "/v1/releases/1.2.3/schemas/incoming/content"}, mapping.SourceSchema)
	require.Equal(t, MappingEngineResponse{ExecutionProfile: "opaque-profile", Language: "opaque-language", LanguageVersion: "2026"}, mapping.Engine)
	condition := ResourceResponse{Name: "condition.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Programs[0].Digest, Size: 3, Href: "/v1/artifacts/rev-mapping/resources/condition.bin"}
	program := ResourceResponse{Name: "transform.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Programs[1].Digest, Size: 4, Href: "/v1/artifacts/rev-mapping/resources/transform.bin"}
	require.Equal(t, &condition, mapping.Programs.Condition)
	require.Equal(t, []MappingTransformationResponse{
		{ResourceResponse: program, TargetSchema: MappingSourceSchemaResponse{ArtifactRevision: "rev-target", ArtifactType: "device", Content: "/v1/releases/1.2.3/schemas/device/content"}, Cardinality: "one"},
		{ResourceResponse: program, TargetSchema: MappingSourceSchemaResponse{ArtifactRevision: "rev-source", ArtifactType: "incoming", Content: "/v1/releases/1.2.3/schemas/incoming/content"}, Cardinality: "many"},
	}, mapping.Programs.Transformations)

	body, err := BuildMappingPackage(value, testVersion)
	require.NoError(t, err)
	require.Equal(t, testVersion, body.SchemaVersion)
	require.Equal(t, mapping, body.Mapping)
	require.Equal(t, []string{"data-center-inventory", "insights-repository"}, body.TargetStores)
	require.Equal(t, map[string]string{
		"self":    "/v1/releases/1.2.3/source-mappings/device-mapping/package",
		"mapping": "/v1/releases/1.2.3/source-mappings/device-mapping", "descriptor": "/v1/artifacts/rev-mapping",
	}, body.Links)
	require.Len(t, body.Programs, 2, "the already-deduplicated shared transformation appears once")
	for index, expected := range value.Programs {
		got := body.Programs[index]
		require.Equal(t, MappingPackageProgramResponse{Name: expected.Name, Role: expected.Role, MediaType: expected.MediaType, Digest: expected.Digest, Size: expected.Size, Content: base64.StdEncoding.EncodeToString(expected.Bytes)}, got)
		decoded, err := base64.StdEncoding.DecodeString(got.Content)
		require.NoError(t, err)
		require.Equal(t, expected.Bytes, decoded)
		require.Equal(t, expected.Digest, fmt.Sprintf("sha256:%x", sha256.Sum256(decoded)))
	}
	require.Len(t, body.Schemas, 2)
	for index, schema := range value.Schemas {
		require.Equal(t, BuildDescriptor(schema, testVersion), body.Schemas[index].Metadata)
		require.Equal(t, schema.Artifact.Payload.Bytes, []byte(body.Schemas[index].Document))
	}
	require.Len(t, body.Mapping.Resources, 3, "the ancillary resource remains metadata-only")
	require.EqualValues(t, 16<<20, body.Mapping.Resources[2].Size)
	require.Less(t, len(testJSON(t, body)), 16<<10)
	require.Equal(t, before, testJSON(t, value))

	requireJSONKeys(t, body, "schemaVersion", "mapping", "programs", "schemas", "targetStores", "links")
	requireJSONKeys(t, mapping, "schemaVersion", "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "targetSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "extensions", "links", "sourceSchema", "engine", "programs")
	requireJSONKeys(t, body.Schemas[0].Metadata, "schemaVersion", "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "schemaClass", "canonicalUri", "payload", "resources", "dependencies", "extensions", "links")
	requireJSONKeys(t, mapping.Payload, "mediaType", "digest", "size", "href")
	requireJSONKeys(t, program, "name", "role", "mediaType", "digest", "size", "href")
	requireJSONKeys(t, mapping.Dependencies[0], "kind", "artifactType", "artifactRevision", "href")
	requireJSONKeys(t, mapping.SourceSchema, "artifactRevision", "artifactType", "content")
	requireJSONKeys(t, mapping.Engine, "executionProfile", "language", "languageVersion")
	requireJSONKeys(t, mapping.Programs, "condition", "transformations")
	requireJSONKeys(t, mapping.Programs.Transformations[0], "name", "role", "mediaType", "digest", "size", "href", "targetSchema", "cardinality")
	requireJSONKeys(t, body.Programs[0], "name", "role", "mediaType", "digest", "size", "content")
	requireJSONKeys(t, body.Schemas[0], "metadata", "document")
}

func TestBuildMappingWithoutConditionOrStores(t *testing.T) {
	value := testPackage(nil)
	value.Detail.Condition = nil
	value.Programs = value.Programs[1:]
	value.Stores = nil
	body, err := BuildMappingPackage(value, testVersion)
	require.NoError(t, err)
	require.Nil(t, body.Mapping.Programs.Condition)
	requireJSONKeys(t, body.Mapping.Programs, "transformations")
	require.NotNil(t, body.TargetStores)
	require.Empty(t, body.TargetStores)
	require.Contains(t, string(testJSON(t, body)), `"targetStores":[]`)

	value.Detail.Transformations = nil
	mapping := BuildMapping(model.Mapping{Descriptor: value.Mapping, Detail: value.Detail}, testVersion)
	require.NotNil(t, mapping.Programs.Transformations)
	require.Empty(t, mapping.Programs.Transformations)
	require.JSONEq(t, `{"transformations":[]}`, string(testJSON(t, mapping.Programs)))
}

func TestMappingPackageSizeMatchesEncodedJSON(t *testing.T) {
	documents := []struct {
		name string
		raw  string
	}{
		{"true", "true"},
		{"false", " false \n"},
		{"empty-object", " { } \n"},
		{"escaped-unicode-object", " {\n\t\"title\": \"<&> 世界 \u2028 \u2029 \\u2028\", \"nested\": [true, false, null] } "},
		{"exact-numbers", ` { "large": 9007199254740993, "decimal": 1.2300000000000000000000001, "exponent": 1e+400, "negativeZero": -0 } `},
	}
	for _, length := range []int{0, 1, 2, 3, 4, 1 << 20} {
		for _, document := range documents {
			t.Run(fmt.Sprintf("bytes-%d/%s", length, document.name), func(t *testing.T) {
				value := testPackage(bytes.Repeat([]byte{0xfb}, length))
				value.Schemas[1].Artifact.Payload = testBytes([]byte(document.raw), "application/schema+json")
				before := testJSON(t, value)
				body, err := BuildMappingPackage(value, testVersion)
				require.NoError(t, err)
				encoded := testJSON(t, body)
				size, err := MappingPackageSize(value, testVersion)
				require.NoError(t, err)
				require.Equal(t, int64(len(encoded)), size)
				require.Equal(t, base64.StdEncoding.EncodedLen(length), len(body.Programs[1].Content))
				require.Equal(t, base64.StdEncoding.EncodeToString(value.Programs[1].Bytes), body.Programs[1].Content)
				require.Equal(t, document.raw, string(body.Schemas[1].Document), "building preserves exact raw document bytes")
				var decoded struct {
					Schemas []struct{ Document json.RawMessage }
				}
				require.NoError(t, json.Unmarshal(encoded, &decoded))
				require.Equal(t, testJSON(t, json.RawMessage(document.raw)), []byte(decoded.Schemas[1].Document))
				require.Equal(t, before, testJSON(t, value))
			})
		}
	}
}

func TestMappingPackageSmallSizeLimits(t *testing.T) {
	value := testPackage([]byte{1, 2, 3, 4})
	body, err := BuildMappingPackage(value, testVersion)
	require.NoError(t, err)
	exact := int64(len(testJSON(t, body)))
	metadata, err := mappingPackageMetadata(value, testVersion)
	require.NoError(t, err)
	for _, limit := range []int64{exact, exact + 1, math.MaxInt64} {
		t.Run(fmt.Sprintf("accept-%d", limit), func(t *testing.T) {
			size, err := mappingPackageSize(value, metadata, limit)
			require.NoError(t, err)
			require.Equal(t, exact, size)
		})
	}
	for _, limit := range []int64{0, 2, 3, 4, 7, 13, exact - 1} {
		t.Run(fmt.Sprintf("reject-%d", limit), func(t *testing.T) {
			size, err := mappingPackageSize(value, metadata, limit)
			require.ErrorIs(t, err, ErrTooLarge)
			require.Zero(t, size)
		})
	}
	size, err := mappingPackageSize(value, metadata, -1)
	require.ErrorIs(t, err, ErrInvalid)
	require.Zero(t, size)
}

func TestMappingPackagePublicSizeBoundaryWithoutProgramAllocation(t *testing.T) {
	require.EqualValues(t, 64<<20, MaxGeneratedJSONBytes)
	value := testPackage(nil)
	value.Programs = value.Programs[1:]
	value.Detail.Condition = nil
	base, err := MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	// Reserve a few metadata bytes for the larger decimal size field.
	value.Programs[0].Size = ((MaxGeneratedJSONBytes - base - 64) / 4) * 3
	size, err := MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	padding := int64(MaxGeneratedJSONBytes) - size
	require.GreaterOrEqual(t, padding, int64(0))
	require.Less(t, padding, int64(64))
	value.Stores[0].Store += strings.Repeat("x", int(padding))
	require.Nil(t, value.Programs[0].Bytes, "the boundary fixture allocates no program bytes")
	size, err = MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	require.EqualValues(t, MaxGeneratedJSONBytes, size)
	_, err = BuildMappingPackage(value, testVersion)
	require.ErrorIs(t, err, ErrInvalid, "exactly fitting metadata passes preflight and reaches the byte-length check")

	value.Stores[0].Store += "x"
	size, err = MappingPackageSize(value, testVersion)
	require.ErrorIs(t, err, ErrTooLarge)
	require.Zero(t, size)
	_, err = BuildMappingPackage(value, testVersion)
	require.ErrorIs(t, err, ErrTooLarge, "oversize preflight runs before Base64 encoding or byte-length checks")
}

func TestMappingPackageRejectsAggregateProgramSize(t *testing.T) {
	value := testPackage(nil)
	program := value.Programs[1]
	program.Size = 16 << 20
	value.Programs = []model.Resource{program, program, program}
	for index := range value.Programs {
		value.Programs[index].Name = fmt.Sprintf("program-%d.bin", index)
	}
	size, err := MappingPackageSize(value, testVersion)
	require.ErrorIs(t, err, ErrTooLarge)
	require.Zero(t, size)
	_, err = BuildMappingPackage(value, testVersion)
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestMappingPackageRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.MappingPackage)
		want   error
	}{
		{"zero-value", func(value *model.MappingPackage) { *value = model.MappingPackage{} }, ErrInvalid},
		{"nil-schemas", func(value *model.MappingPackage) { value.Schemas = nil }, ErrInvalid},
		{"empty-schemas", func(value *model.MappingPackage) { value.Schemas = []model.Descriptor{} }, ErrInvalid},
		{"nil-programs", func(value *model.MappingPackage) { value.Programs = nil }, ErrInvalid},
		{"empty-programs", func(value *model.MappingPackage) { value.Programs = []model.Resource{} }, ErrInvalid},
		{"nil-schema-bytes", func(value *model.MappingPackage) { value.Schemas[0].Artifact.Payload.Bytes = nil }, ErrInvalid},
		{"empty-schema-bytes", func(value *model.MappingPackage) { value.Schemas[0].Artifact.Payload.Bytes = []byte{} }, ErrInvalid},
		{"invalid-schema", func(value *model.MappingPackage) { value.Schemas[0].Artifact.Payload.Bytes = []byte(`{"type":`) }, ErrInvalid},
		{"trailing-schema-value", func(value *model.MappingPackage) { value.Schemas[0].Artifact.Payload.Bytes = []byte(`true false`) }, ErrInvalid},
		{"invalid-mapping-extensions", func(value *model.MappingPackage) { value.Mapping.Artifact.Extensions = []byte(`{`) }, ErrInvalid},
		{"invalid-schema-extensions", func(value *model.MappingPackage) { value.Schemas[1].Artifact.Extensions = []byte(`undefined`) }, ErrInvalid},
		{"negative-program-size", func(value *model.MappingPackage) { value.Programs[1].Size = -1 }, ErrInvalid},
		{"minimum-program-size", func(value *model.MappingPackage) { value.Programs[1].Size = math.MinInt64 }, ErrInvalid},
		{"maximum-program-size", func(value *model.MappingPackage) { value.Programs[1].Size = math.MaxInt64 }, ErrTooLarge},
		{"single-oversize-program", func(value *model.MappingPackage) { value.Programs[1].Size = MaxGeneratedJSONBytes + 1 }, ErrTooLarge},
		{"base64-expansion-oversize", func(value *model.MappingPackage) { value.Programs[1].Size = MaxGeneratedJSONBytes }, ErrTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := testPackage([]byte{1, 2, 3})
			test.change(&value)
			size, err := MappingPackageSize(value, testVersion)
			require.ErrorIs(t, err, test.want)
			require.Zero(t, size)
			body, err := BuildMappingPackage(value, testVersion)
			require.ErrorIs(t, err, test.want)
			require.Equal(t, MappingPackageResponse{}, body)
		})
	}
}

func TestMappingPackagePropagatesMetadataMarshalError(t *testing.T) {
	value := testPackage(nil)
	value.Mapping.Artifact.Extensions = []byte(`{"broken":`)
	for _, build := range []bool{false, true} {
		t.Run(fmt.Sprintf("build-%t", build), func(t *testing.T) {
			var err error
			if build {
				_, err = BuildMappingPackage(value, testVersion)
			} else {
				_, err = MappingPackageSize(value, testVersion)
			}
			require.ErrorIs(t, err, ErrInvalid)
			var marshalerError *json.MarshalerError
			require.True(t, errors.As(err, &marshalerError), "the metadata encoding error remains available to callers")
			require.Error(t, marshalerError.Err)
		})
	}
}

func TestBuildMappingPackageRejectsMismatchedProgramBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		size int64
		raw  []byte
	}{
		{"missing-bytes", 1, nil},
		{"short-bytes", 4, []byte{1, 2, 3}},
		{"extra-bytes", 2, []byte{1, 2, 3}},
		{"nonempty-zero-size", 0, []byte{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := testPackage(test.raw)
			value.Programs[1].Size = test.size
			size, err := MappingPackageSize(value, testVersion)
			require.NoError(t, err, "counting uses declared program sizes without requiring their bytes")
			require.Positive(t, size)
			body, err := BuildMappingPackage(value, testVersion)
			require.ErrorIs(t, err, ErrInvalid)
			require.Equal(t, MappingPackageResponse{}, body)
		})
	}
}

func TestBuildMappingPackageCopiesMutableJSON(t *testing.T) {
	value := testPackage([]byte{1, 2, 3})
	before := testJSON(t, value)
	body, err := BuildMappingPackage(value, testVersion)
	require.NoError(t, err)
	body.Mapping.Extensions[0] = '['
	body.Schemas[0].Metadata.Extensions[0] = '['
	body.Schemas[0].Document[0] = 'f'
	body.Mapping.Resources[0].Name = "changed"
	body.Mapping.Dependencies[0].ArtifactType = "changed"
	body.Mapping.Programs.Transformations[0].Name = "changed"
	body.Mapping.Programs.Condition.Name = "changed"
	body.Programs[0].Name = "changed"
	body.TargetStores[0] = "changed"
	body.Mapping.Links["self"] = "changed"
	body.Links["self"] = "changed"
	require.Equal(t, before, testJSON(t, value), "output mutations must not change immutable domain inputs")
}

func TestBuildAndCountConcurrentImmutableInput(t *testing.T) {
	value := testPackage(bytes.Repeat([]byte{0, 0xff, 0x80}, 1024))
	before := testJSON(t, value)
	expected, err := BuildMappingPackage(value, testVersion)
	require.NoError(t, err)
	encoded := testJSON(t, expected)
	t.Run("concurrent", func(t *testing.T) {
		for index := range 12 {
			t.Run(fmt.Sprintf("reader-%d", index), func(t *testing.T) {
				t.Parallel()
				for range 5 {
					size, err := MappingPackageSize(value, testVersion)
					require.NoError(t, err)
					require.Equal(t, int64(len(encoded)), size)
					body, err := BuildMappingPackage(value, testVersion)
					require.NoError(t, err)
					require.Equal(t, encoded, testJSON(t, body))
					// Mutating one result must not affect other builders or the input.
					body.Mapping.Extensions[0] = '['
					body.Schemas[0].Document[0] = 'f'
					body.Links["self"] = "changed"
				}
			})
		}
	})
	require.Equal(t, before, testJSON(t, value))
}
