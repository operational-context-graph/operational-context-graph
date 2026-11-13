// Package manifest parses and validates immutable Schema Release Manifests in format 1.0.0. A manifest selects
// artifact revisions, binds their byte descriptors, and declares release-scoped catalogs and provenance.
//
// This implementation enforces the acceptance profile for schema release of the Schema Registry implementation in
// this repository, not an unbounded general-format parser. This acceptance profile includes size limits and naming
// and URI restrictions.
//
// # Parsing and canonicalization
//
// [Parse] accepts only JSON Canonicalization Scheme (JCS) bytes, as defined by RFC 8785. It rejects a byte order mark,
// trailing newline, unknown format fields, and incorrectly ordered manifest collections. It retains an independent
// copy of the submitted bytes in [Manifest.Raw]. [ParseFromFile] applies the same checks to a caller-selected file.
// Neither function loads the artifact files named by bundle paths.
//
// [Canonicalize] prepares publisher input by sorting format-defined collections and producing JCS bytes. It does not
// repair invalid metadata, remove duplicates, or reorder publisher extension arrays. Artifact payloads remain separate,
// exact byte representations; canonicalization does not rewrite them.
//
// # Full validation
//
// [Validate] combines manifest parsing with manifest validation. Callers MUST supply exact selected artifact bytes and
// a trusted, previously validated history through [Context]. [Manifest.Validate] checks descriptor integrity, payload
// formats, dependency closure, catalog targets, storage placement, and source-mapping agreement. It also checks
// immutable revision reuse and predecessor bindings against the supplied history. Document references resolve only
// through the release catalog; validation never fetches missing documents.
// Changed JSON aliases require historical bytes to establish schema usage and context-only exceptions.
// This proof checks historical byte integrity without revalidating historical lineage or change summaries.
//
// When [Changes] are present, validation compares them through [Diff] and enforces the corresponding version increment.
// Without [Changes], validation still rejects a non-initial release with no effective difference. It does not enforce
// a compatibility-based version increment in that case. Registry acceptance MUST separately check application response
// serviceability.
//
// # Comparison and errors
//
// [Diff] compares validated endpoint selections rather than accumulating changes across intermediate releases. It
// classifies uncertain compatibility as breaking and needs exact bytes to prove supported non-breaking exceptions.
// It does not validate endpoints or determine initial-release status from [Context.History].
//
// [Manifest]-specific validation errors wrap [ErrInvalid]. Parsing errors may instead wrap 'strictjson' errors or
// originate from the canonicalization library. Callers SHOULD inspect errors with [errors.Is] rather than matching
// diagnostic text.
package manifest
