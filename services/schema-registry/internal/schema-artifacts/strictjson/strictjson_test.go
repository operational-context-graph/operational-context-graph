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
)

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

func tooManyMembers() []byte {
	fields := make([]string, MaxObjectMembers+1)
	for index := range fields {
		fields[index] = `"field` + strconv.Itoa(index) + `":0`
	}
	return []byte("{" + strings.Join(fields, ",") + "}")
}

func TestParseAcceptsBoundaries(t *testing.T) {
	value, err := Parse([]byte(`{"integer":9007199254740991,"fraction":1.5,"array":[]}`))
	require.NoError(t, err)
	assert.IsType(t, map[string]any{}, value)
	_, err = Parse(make([]byte, MaxBytes+1))
	assert.ErrorIs(t, err, ErrTooLarge)
}

func TestParseStructuralLimits(t *testing.T) {
	tooDeep := strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1)
	_, err := Parse([]byte(tooDeep))
	assert.ErrorIs(t, err, ErrDepth)

	tooWide := "[" + strings.Repeat("0,", MaxArrayEntries) + "0]"
	_, err = Parse([]byte(tooWide))
	assert.ErrorIs(t, err, ErrArrayEntries)

	tooLong := `{"value":"` + strings.Repeat("a", MaxStringBytes+1) + `"}`
	_, err = Parse([]byte(tooLong))
	assert.ErrorIs(t, err, ErrStringTooLarge)
}

func TestRejectUnknownFields(t *testing.T) {
	require.NoError(t, RejectUnknownFields(map[string]any{"id": "value"}, "id"))
	assert.Error(t, RejectUnknownFields(map[string]any{"other": "value"}, "id"))
}

func FuzzParse(f *testing.F) {
	for _, input := range [][]byte{[]byte(`{}`), []byte(`[]`), []byte(`{"id":"value"}`), []byte{0xff}} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = Parse(input)
	})
}

func BenchmarkParse(b *testing.B) {
	input := []byte(`{"metadata":{"name":"device"},"values":[1,2,3],"active":true}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		if _, err := Parse(input); err != nil {
			b.Fatal(err)
		}
	}
}
