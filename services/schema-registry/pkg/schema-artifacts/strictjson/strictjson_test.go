// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package strictjson

import (
	"encoding/json"
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
		{"whitespace", []byte(" \t\r\n"), ErrMalformed},
		{"byte order mark", append([]byte{0xef, 0xbb, 0xbf}, []byte(`{}`)...), ErrMalformed},
		{"trailing value", []byte(`{} true`), ErrMalformed},
		{"trailing syntax", []byte(`{} !`), ErrMalformed},
		{"missing object close", []byte(`{"a":1`), ErrMalformed},
		{"missing array close", []byte(`[1`), ErrMalformed},
		{"invalid member name", []byte(`{1:2}`), ErrMalformed},
		{"invalid array value", []byte(`[!]`), ErrMalformed},
		{"invalid UTF-8", []byte{'{', '"', 0xff, '"', ':', '1', '}'}, ErrMalformed},
		{"duplicate", []byte(`{"a":1,"a":2}`), ErrDuplicate},
		{"escaped duplicate", []byte(`{"a":1,"\u0061":2}`), ErrDuplicate},
		{"nested duplicate", []byte(`[{"a":1,"a":2}]`), ErrDuplicate},
		{"unsafe integer", []byte(`9007199254740992`), ErrNumber},
		{"unsafe negative integer", []byte(`-9007199254740992`), ErrNumber},
		{"unsafe exponent integer", []byte(`9007199254740992e0`), ErrNumber},
		{"unsafe decimal integer", []byte(`9007199254740992.0`), ErrNumber},
		{"float overflow", []byte(`1e309`), ErrNumber},
		{"too many members", tooManyMembers(), ErrObjectMembers},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := Parse(test.input)
			require.ErrorIs(t, err, test.err)
			assert.Nil(t, value)
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
	assert.Equal(t, map[string]any{"integer": json.Number("9007199254740991"), "fraction": json.Number("1.5"), "array": []any{}}, value)
	_, err = Parse(make([]byte, schemaartifacts.MaxArtifactBytes+1))
	assert.ErrorIs(t, err, ErrTooLarge)
}

// TestParsePreservesValues checks all JSON value types, exact number spelling, and independent object member scopes.
func TestParsePreservesValues(t *testing.T) {
	for _, test := range []struct {
		input string
		want  any
	}{
		{`null`, nil}, {`true`, true}, {`false`, false}, {`"é\n"`, "é\n"},
		{`-9007199254740991`, json.Number("-9007199254740991")},
		{`-0`, json.Number("-0")}, {`1.25e-3`, json.Number("1.25e-3")},
		{`{}`, map[string]any{}}, {`[]`, []any{}},
		{` {"a":1,"child":{"a":2}} `, map[string]any{"a": json.Number("1"), "child": map[string]any{"a": json.Number("2")}}},
	} {
		t.Run(test.input, func(t *testing.T) {
			value, err := Parse([]byte(test.input))
			require.NoError(t, err)
			assert.Equal(t, test.want, value)
		})
	}
}

// TestObject requires an object root and preserves errors from strict parsing.
func TestObject(t *testing.T) {
	object, err := Object([]byte(`{"value":true}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": true}, object)
	for _, input := range []string{`[]`, `null`, `true`, `1`, `"value"`, `{`} {
		t.Run(input, func(t *testing.T) {
			object, parseErr := Object([]byte(input))
			require.ErrorIs(t, parseErr, ErrMalformed)
			assert.Nil(t, object)
		})
	}
	_, err = Object([]byte(`{"a":1,"a":2}`))
	require.ErrorIs(t, err, ErrDuplicate)
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
