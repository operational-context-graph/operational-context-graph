// Package limits defines fixed Schema Introspection API serviceability limits.
package limits

// MaxGeneratedJSONBytes bounds each generated identity JSON representation.
const MaxGeneratedJSONBytes int64 = 64 * 1024 * 1024
