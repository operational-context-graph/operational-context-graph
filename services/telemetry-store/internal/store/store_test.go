// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Date(2026, 3, 15, 10, 30, 0, 123456000, time.UTC)
	original := Cursor{TS: ts, IRI: "urn:ocg:log:opensearch:01HX5Y2K6P3M4N8Q7R"}
	encoded := original.Encode()
	assert.NotEmpty(t, encoded)

	decoded, err := DecodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, original.IRI, decoded.IRI)
	assert.True(t, original.TS.Equal(decoded.TS))
}

func TestDecodeCursorInvalid(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestClampLimit(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 1000},
		{-1, 1000},
		{-100, 1000},
		{1, 1},
		{500, 500},
		{10000, 10000},
		{10001, 1000},
		{99999, 1000},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, clampLimit(tc.in), "clampLimit(%d)", tc.in)
	}
}

func TestDecodeCursor_CorruptJSON(t *testing.T) {
	// Valid base64 but not valid JSON inside.
	import64 := "bm90LWpzb24="
	_, err := DecodeCursor(import64)
	assert.Error(t, err)
}

func TestCursorEncode_PreservesNanosecondPrecision(t *testing.T) {
	ts := time.Date(2026, 6, 1, 12, 0, 0, 123456789, time.UTC)
	c := Cursor{TS: ts, IRI: "urn:ocg:log:test:ns"}
	decoded, err := DecodeCursor(c.Encode())
	require.NoError(t, err)
	// JSON marshals time.Time with sub-second precision; round-trip must be exact.
	assert.True(t, ts.Equal(decoded.TS))
}
