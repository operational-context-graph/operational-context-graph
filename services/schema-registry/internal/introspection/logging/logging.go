// Package logging configures JSON logs that bound request-derived string fields.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

// New returns a JSON logger. slog escapes JSON strings and this hook truncates
// untrusted values before they reach the handler.
func New(cfg introspection.LoggingConfig) *slog.Logger {
	return newLogger(cfg, os.Stderr)
}

// newLogger creates a severity-filtered JSON logger that bounds string attributes before encoding.
func newLogger(cfg introspection.LoggingConfig, writer io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: parseLevel(cfg.Level),
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Value.Kind() == slog.KindString {
				attr.Value = slog.StringValue(truncate(attr.Value.String(), cfg.MaxFieldLength))
			}
			return attr
		},
	}))
}

// parseLevel maps configured severity names to slog levels, defaulting unknown values to info.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// truncate limits a string's byte length and removes incomplete UTF-8 sequences at the boundary.
// max MUST be nonnegative.
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	// A byte limit can split a multibyte rune; discard the fragment rather than emitting invalid text.
	return strings.ToValidUTF8(value[:max], "")
}
