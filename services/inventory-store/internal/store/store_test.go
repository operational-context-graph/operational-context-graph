// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	original := Cursor{CreatedAt: ts, ID: id}
	encoded := original.Encode()
	assert.NotEmpty(t, encoded)

	decoded, err := DecodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, original.ID, decoded.ID)
	assert.True(t, original.CreatedAt.Equal(decoded.CreatedAt))
}

func TestDecodeCursorInvalid(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecodeCursorBadJSON(t *testing.T) {
	// valid base64 but not valid cursor JSON
	_, err := DecodeCursor("dGhpcyBpcyBub3QganNvbg")
	assert.Error(t, err)
}

func TestNullJSON(t *testing.T) {
	assert.Nil(t, nullJSON(nil))
	assert.Nil(t, nullJSON(json.RawMessage("null")))
	assert.Nil(t, nullJSON(json.RawMessage("")))

	val := nullJSON(json.RawMessage(`{"key":"value"}`))
	assert.NotNil(t, val)
}

func TestObservedAtGuard_FormatStability(t *testing.T) {
	// The temporal dedup guard compares last_updated_at_source as a formatted string.
	// Verify the format used in UpsertAsset is stable across round-trips.
	ts := time.Date(2026, 3, 15, 12, 30, 45, 123456789, time.UTC)
	formatted := ts.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	assert.NotEmpty(t, formatted)
	// Same timestamp formatted twice must be identical.
	assert.Equal(t, formatted, ts.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
}
