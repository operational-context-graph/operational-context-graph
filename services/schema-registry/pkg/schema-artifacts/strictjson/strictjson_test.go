// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package strictjson

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// TestParseRejectsInvalidInput verifies error classification for malformed encoding, duplicate members, unsafe numbers, and oversized objects.
func TestParseRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		err   error
	}{
		{"nil", nil, ErrMalformed},
		{"empty", []byte{}, ErrMalformed},
		{"malformed", []byte(`{"a":`), ErrMalformed},
		{"invalid UTF-8", []byte{'{', '"', 0xff, '"', ':', '1', '}'}, ErrMalformed},
		{"duplicate", []byte(`{"a":1,"a":2}`), ErrDuplicate},
		{"unsafe integer", []byte(`9007199254740992`), ErrNumber},
		{"too many members", tooManyMembers(), ErrObjectMembers},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.input)
			require.Error(t, err)
			assert.True(t, errors.Is(err, test.err), err)
		})
	}
}

// tooManyMembers constructs valid JSON with one more object member than the profile permits.
func tooManyMembers() []byte {
	fields := make([]string, schemaartifacts.MaxObjectMembers+1)
	for index := range fields {
		fields[index] = `"field` + strconv.Itoa(index) + `":0`
	}
	return []byte("{" + strings.Join(fields, ",") + "}")
}

// TestParseAcceptsBoundaries checks exact-range integers, fractional values, empty arrays, and the total document byte limit.
func TestParseAcceptsBoundaries(t *testing.T) {
	value, err := Parse([]byte(`{"integer":9007199254740991,"fraction":1.5,"array":[]}`))
	require.NoError(t, err)
	assert.IsType(t, map[string]any{}, value)
	_, err = Parse(make([]byte, schemaartifacts.MaxArtifactBytes+1))
	assert.ErrorIs(t, err, ErrTooLarge)
}

// TestParseStructuralLimits checks independent depth, array-entry, and decoded-string limits.
func TestParseStructuralLimits(t *testing.T) {
	tooDeep := strings.Repeat("[", schemaartifacts.MaxJSONDepth+1) + "0" + strings.Repeat("]", schemaartifacts.MaxJSONDepth+1)
	_, err := Parse([]byte(tooDeep))
	assert.ErrorIs(t, err, ErrDepth)

	tooWide := "[" + strings.Repeat("0,", schemaartifacts.MaxArrayEntries) + "0]"
	_, err = Parse([]byte(tooWide))
	assert.ErrorIs(t, err, ErrArrayEntries)

	tooLong := `{"value":"` + strings.Repeat("a", schemaartifacts.MaxJSONStringBytes+1) + `"}`
	_, err = Parse([]byte(tooLong))
	assert.ErrorIs(t, err, ErrStringTooLarge)
}

// TestParseArtifactProfileBoundaries checks exact and excessive sizes against the shared artifact profile.
func TestParseArtifactProfileBoundaries(t *testing.T) {
	fields := make([]string, schemaartifacts.MaxObjectMembers+1)
	for index := range fields {
		fields[index] = `"field` + strconv.Itoa(index) + `":0`
	}
	tests := []struct {
		name  string
		input string
		err   error
	}{
		{"exact bytes", "true" + strings.Repeat(" ", schemaartifacts.MaxArtifactBytes-4), nil},
		{"excess bytes", "true" + strings.Repeat(" ", schemaartifacts.MaxArtifactBytes-3), ErrTooLarge},
		{"exact depth", strings.Repeat("[", schemaartifacts.MaxJSONDepth) + "0" + strings.Repeat("]", schemaartifacts.MaxJSONDepth), nil},
		{"excess depth", strings.Repeat("[", schemaartifacts.MaxJSONDepth+1) + "0" + strings.Repeat("]", schemaartifacts.MaxJSONDepth+1), ErrDepth},
		{"exact members", "{" + strings.Join(fields[:schemaartifacts.MaxObjectMembers], ",") + "}", nil},
		{"excess members", "{" + strings.Join(fields, ",") + "}", ErrObjectMembers},
		{"exact entries", "[" + strings.Repeat("0,", schemaartifacts.MaxArrayEntries-1) + "0]", nil},
		{"excess entries", "[" + strings.Repeat("0,", schemaartifacts.MaxArrayEntries) + "0]", ErrArrayEntries},
		{"exact string", `"` + strings.Repeat("é", schemaartifacts.MaxJSONStringBytes/2) + `"`, nil},
		{"excess string", `"` + strings.Repeat("é", schemaartifacts.MaxJSONStringBytes/2) + `a"`, ErrStringTooLarge},
		{"exact member name", `{"` + strings.Repeat("a", schemaartifacts.MaxJSONStringBytes) + `":0}`, nil},
		{"excess member name", `{"` + strings.Repeat("a", schemaartifacts.MaxJSONStringBytes+1) + `":0}`, ErrStringTooLarge},
		{"escaped string", `"` + strings.Repeat(`\u0061`, schemaartifacts.MaxJSONStringBytes) + `"`, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.input))
			require.ErrorIs(t, err, test.err)
		})
	}
}

// TestRejectUnknownFields verifies that object-shape validation accepts only explicitly allowed member names.
func TestRejectUnknownFields(t *testing.T) {
	require.NoError(t, RejectUnknownFields(map[string]any{"id": "value"}, "id"))
	assert.Error(t, RejectUnknownFields(map[string]any{"other": "value"}, "id"))
}

// FuzzParse exercises parser robustness across valid seeds and arbitrary malformed byte input.
func FuzzParse(f *testing.F) {
	for _, input := range [][]byte{[]byte(`{}`), []byte(`[]`), []byte(`{"id":"value"}`), []byte{0xff}} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = Parse(input)
	})
}
