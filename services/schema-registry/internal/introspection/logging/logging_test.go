// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

func TestLoggerEscapesAndTruncatesStringFields(t *testing.T) {
	var output bytes.Buffer
	logger := newLogger(introspection.LoggingConfig{Level: "info", MaxFieldLength: 4}, &output)
	logger.Info("request", "path", "\"\nabcdef")

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, "\"\nab", record["path"])
}

func TestTruncatePreservesValidUTF8(t *testing.T) {
	assert.Equal(t, "a", truncate("a€", 2))
}
