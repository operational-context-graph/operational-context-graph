// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package logging configures JSON logs that bound request-derived string fields.
package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

// New returns a JSON logger. slog escapes JSON strings and this hook truncates
// untrusted values before they reach the handler.
func New(cfg introspection.LoggingConfig) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(cfg.Level),
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Value.Kind() == slog.KindString {
				attr.Value = slog.StringValue(truncate(attr.Value.String(), cfg.MaxFieldLength))
			}
			return attr
		},
	}))
}

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

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return strings.ToValidUTF8(value[:max], "")
}
