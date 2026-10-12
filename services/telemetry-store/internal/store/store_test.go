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
	original := Cursor{TS: ts, RecordID: "01HX5Y2K6P3M4N8Q7R"}
	encoded := original.Encode()
	assert.NotEmpty(t, encoded)

	decoded, err := DecodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, original.RecordID, decoded.RecordID)
	assert.True(t, original.TS.Equal(decoded.TS))
}

func TestDecodeCursorInvalid(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}
