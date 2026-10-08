// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDorisTimeWithMicros(t *testing.T) {
	got, err := parseDorisTime("2026-03-15 10:30:00.123456")
	require.NoError(t, err)
	want := time.Date(2026, 3, 15, 10, 30, 0, 123456000, time.UTC)
	assert.True(t, want.Equal(got), "got %v, want %v", got, want)
}

func TestParseDorisTimeWithoutMicros(t *testing.T) {
	got, err := parseDorisTime("2026-03-15 10:30:00")
	require.NoError(t, err)
	want := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	assert.True(t, want.Equal(got), "got %v, want %v", got, want)
}

func TestParseDorisTimeInvalid(t *testing.T) {
	_, err := parseDorisTime("not-a-date")
	assert.Error(t, err)
}
