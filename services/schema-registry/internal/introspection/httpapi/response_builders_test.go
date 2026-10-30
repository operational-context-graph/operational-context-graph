// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/api/v1"
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
				expected := v1.DescriptorResponse{
					SchemaVersion: version, Kind: kind, ArtifactType: "device-mapping", ArtifactRevision: "rev-mapping",
					CreatedUnderSchemaVersion: "1.0.0", TargetSchemaVersion: value.Artifact.TargetSchemaVersion,
					CanonicalURI: "https://example.test/device-mapping",
					Payload:      v1.PayloadResponse{MediaType: "application/json", Digest: value.Artifact.Payload.Digest, Size: 19, Href: "/v1/artifacts/rev-mapping"},
					Resources: []v1.ResourceResponse{
						{Name: "condition.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Resources[0].Digest, Size: 3, Href: "/v1/artifacts/rev-mapping/resources/condition.bin"},
						{Name: "transform.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Resources[1].Digest, Size: 0, Href: "/v1/artifacts/rev-mapping/resources/transform.bin"},
						{Name: "ancillary.bin", Role: "attachment", MediaType: "application/octet-stream", Digest: "sha256:metadata-only", Size: 16 << 20, Href: "/v1/artifacts/rev-mapping/resources/ancillary.bin"},
					},
					Dependencies: []v1.DependencyResponse{
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
	require.Equal(t, v1.MappingSourceSchemaResponse{ArtifactRevision: "rev-source", ArtifactType: "incoming", Content: "/v1/releases/1.2.3/schemas/incoming/content"}, mapping.SourceSchema)
	require.Equal(t, v1.MappingEngineResponse{ExecutionProfile: "opaque-profile", Language: "opaque-language", LanguageVersion: "2026"}, mapping.Engine)
	condition := v1.ResourceResponse{Name: "condition.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Programs[0].Digest, Size: 3, Href: "/v1/artifacts/rev-mapping/resources/condition.bin"}
	program := v1.ResourceResponse{Name: "transform.bin", Role: "program", MediaType: "application/octet-stream", Digest: value.Programs[1].Digest, Size: 4, Href: "/v1/artifacts/rev-mapping/resources/transform.bin"}
	require.Equal(t, &condition, mapping.Programs.Condition)
	require.Equal(t, []v1.MappingTransformationResponse{
		{ResourceResponse: program, TargetSchema: v1.MappingSourceSchemaResponse{ArtifactRevision: "rev-target", ArtifactType: "device", Content: "/v1/releases/1.2.3/schemas/device/content"}, Cardinality: "one"},
		{ResourceResponse: program, TargetSchema: v1.MappingSourceSchemaResponse{ArtifactRevision: "rev-source", ArtifactType: "incoming", Content: "/v1/releases/1.2.3/schemas/incoming/content"}, Cardinality: "many"},
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
		require.Equal(t, v1.MappingPackageProgramResponse{Name: expected.Name, Role: expected.Role, MediaType: expected.MediaType, Digest: expected.Digest, Size: expected.Size, Content: base64.StdEncoding.EncodeToString(expected.Bytes)}, got)
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
			require.Equal(t, v1.MappingPackageResponse{}, body)
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
