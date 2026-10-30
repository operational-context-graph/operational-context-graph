// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/api/v1"
	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/stretchr/testify/require"
)

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
	require.EqualValues(t, 64<<20, config.MaxGeneratedJSONBytes)
	value := testPackage(nil)
	value.Programs = value.Programs[1:]
	value.Detail.Condition = nil
	base, err := MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	// Reserve a few metadata bytes for the larger decimal size field.
	value.Programs[0].Size = ((config.MaxGeneratedJSONBytes - base - 64) / 4) * 3
	size, err := MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	padding := config.MaxGeneratedJSONBytes - size
	require.GreaterOrEqual(t, padding, int64(0))
	require.Less(t, padding, int64(64))
	value.Stores[0].Store += strings.Repeat("x", int(padding))
	require.Nil(t, value.Programs[0].Bytes, "the boundary fixture allocates no program bytes")
	size, err = MappingPackageSize(value, testVersion)
	require.NoError(t, err)
	require.EqualValues(t, config.MaxGeneratedJSONBytes, size)
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
		{"single-oversize-program", func(value *model.MappingPackage) { value.Programs[1].Size = config.MaxGeneratedJSONBytes + 1 }, ErrTooLarge},
		{"base64-expansion-oversize", func(value *model.MappingPackage) { value.Programs[1].Size = config.MaxGeneratedJSONBytes }, ErrTooLarge},
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
			require.Equal(t, v1.MappingPackageResponse{}, body)
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
