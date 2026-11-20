package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
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

func TestLoggerFiltersConfiguredLevels(t *testing.T) {
	for _, test := range []struct {
		name    string
		minimum slog.Level
	}{
		{name: "debug", minimum: slog.LevelDebug},
		{name: "info", minimum: slog.LevelInfo},
		{name: "warn", minimum: slog.LevelWarn},
		{name: "error", minimum: slog.LevelError},
		{name: "unknown", minimum: slog.LevelInfo},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := newLogger(introspection.LoggingConfig{Level: test.name, MaxFieldLength: 100}, &output)
			for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
				output.Reset()
				logger.Log(t.Context(), level, "message", "count", 7)
				if level < test.minimum {
					assert.Empty(t, output.String())
					continue
				}
				var record map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &record))
				assert.Equal(t, "message", record["msg"])
				assert.EqualValues(t, 7, record["count"])
			}
		})
	}
	assert.NotNil(t, New(introspection.LoggingConfig{Level: "info", MaxFieldLength: 100}))
}

func TestTruncateBoundaries(t *testing.T) {
	for _, test := range []struct {
		value, want string
		limit       int
	}{
		{value: "", want: "", limit: 0},
		{value: "short", want: "short", limit: 10},
		{value: "exact", want: "exact", limit: 5},
		{value: "€x", want: "€", limit: 3},
		{value: "€x", want: "", limit: 2},
	} {
		assert.Equal(t, test.want, truncate(test.value, test.limit))
	}
}
