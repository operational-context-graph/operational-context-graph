// Package schemaartifacts defines shared validation limits for version 1 Schema Registry artifacts and release
// manifests. Limits count bytes, not characters, unless a constant states otherwise. These limits describe individual
// documents and collections, not a release's aggregate memory or storage use.
//
// # Package organization
//
// The 'manifest' subpackage parses, canonicalizes, validates, and compares Schema Release Manifests.
// The 'artifact' subpackage models immutable artifact revisions and verifies their descriptors against exact bytes.
// The 'sourcemapping' subpackage validates mapping descriptors, program bindings, and schema selectors without
// executing programs.
// The 'strictjson' subpackage enforces bounded Internet JSON parsing.
// The 'offline' subpackage restricts JSON Schema and JSON-LD document lookups to caller-supplied catalogs.
//
// # Validation boundaries
//
// Parsing a manifest checks its representation and metadata, not the availability or integrity of selected artifact
// bytes. Full manifest validation requires caller-supplied artifact bytes, the Registry origin, and previously
// validated release history. Validation does not retrieve missing artifacts, discover predecessors, or execute
// mapping programs. Applications MUST separately enforce their filesystem boundaries, aggregate resource budgets,
// and response representation limits.
package schemaartifacts
