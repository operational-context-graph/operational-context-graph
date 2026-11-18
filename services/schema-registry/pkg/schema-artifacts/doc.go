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
// Schema release manifest parsing checks canonical UTF-8 JSON, known fields, required metadata, and strictly ordered
// format-defined arrays. It does not establish complete descriptor validity, reference resolution, or selected artifact
// byte integrity. Full validation requires caller-supplied artifact bytes, the Registry origin, and previously validated
// release history.
//
// Full validation checks:
//
//   - Descriptor digests and sizes against exact bytes, and dependencies and catalog targets against the release's
//     selected artifacts.
//   - Binding targets, class-appropriate storage placement, and program resource names and digests.
//   - Revision immutability and predecessor versions and digests.
//   - Present change summaries against the deterministic predecessor comparison and the corresponding schema version
//     increment.
//
// Validation does not retrieve missing artifacts, discover predecessors, verify Git provenance, or execute source mapping
// programs.
package schemaartifacts
