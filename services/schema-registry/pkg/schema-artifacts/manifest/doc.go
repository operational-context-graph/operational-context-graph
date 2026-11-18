// Package manifest parses and validates immutable Schema Release Manifests in format 1.0.0. A manifest selects
// artifact revisions, binds their byte descriptors, and declares release-scoped catalogs and provenance.
//
// This implementation enforces the acceptance profile for schema release of the Schema Registry implementation in
// this repository, not an unbounded general-format parser. This acceptance profile includes size limits and naming
// and URI restrictions.
//
// # Parsing and canonicalization
//
// [Parse] checks the manifest representation and metadata. Its checks cover:
//
//   - A UTF-8 Internet JSON (I-JSON) object within the Registry's document and collection limits.
//   - Exact JSON Canonicalization Scheme (JCS) bytes, as defined by RFC 8785, without duplicate members or surrounding
//     whitespace.
//   - Known fields, required values, non-empty strings, format declarations, metadata syntax, and operation-specific
//     change shapes.
//   - Unique payload and resource bundle paths across the release.
//   - Strictly increasing UTF-16 sort keys in format-defined arrays, excluding duplicate keys without normalizing strings.
//   - Reverse-domain syntax for extension names.
//
// Parsing does not establish complete descriptor validity, reference resolution, byte integrity, or historical
// immutability and predecessor bindings.
// [Parse] documents the precise metadata checks and retains an independent copy of submitted bytes in [Manifest.Raw].
// [ParseFromFile] applies the same checks to a caller-selected file; neither function loads referenced artifact files.
//
// [Canonicalize] prepares publisher input by sorting format-defined collections and producing JCS bytes. It does not
// repair invalid metadata, remove duplicates, or reorder publisher extension arrays. Artifact payloads remain separate,
// exact byte representations; canonicalization does not rewrite them.
//
// # Full validation
//
// [Validate] combines manifest parsing with manifest validation. Callers MUST supply exact selected artifact bytes and
// a trusted, previously validated history through [Context].
//
// [Manifest.Validate] checks selected bytes and release closure. It checks that:
//
//   - Descriptor digests and sizes match exact bytes, and supplied metadata matches every selection.
//   - Canonical URIs identify exact revisions under the configured Registry origin.
//   - Dependencies, bindings, and catalog targets select the appropriate artifacts or owned resources within this release.
//   - JSON Schema identifiers select their owning payloads or resources in the catalog.
//   - Runtime document references resolve through the catalog, and cross-artifact references have exact declared
//     dependencies.
//   - Schema payloads compile as JSON Schema Draft 2020-12, and context payloads pass JSON-LD validation and media type
//     checks.
//   - Canonical schemas have storage entries using registered stores, and source-record schemas have none.
//
// Source mapping checks cover:
//
//   - Schema selectors resolving to declared dependencies and revisions selected by this release.
//   - Target versions identifying this release or an ancestor.
//   - Program references selecting owned resources with matching names, roles, and digests, with every program resource
//     referenced.
//
// Historical validation checks that:
//
//   - Predecessor versions and digests identify the latest supplied release, and candidate schema versions do not repeat
//     historical versions.
//   - Reused revisions preserve revision-bound metadata, excluding bundle paths, and retired logical identities do not
//     reappear.
//   - New revisions declare this release as their creation version.
//   - Historical canonical URIs and schema identifiers preserve their bound content.
//
// Changed JSON aliases require historical bytes to establish schema usage and context-only exceptions.
// This proof checks historical byte integrity without revalidating historical lineage or change summaries.
// Document references resolve only through the release catalog; validation never fetches missing documents.
// Program behavior and condition overlap are not checked.
//
// When [Changes] are present, validation compares them through [Diff] and enforces the corresponding version increment.
// Without [Changes], validation still rejects a non-initial release with no effective difference. It does not enforce
// a compatibility-based version increment in that case. Registry acceptance MUST separately check application response
// serviceability.
//
// Git repository availability, commit existence, format-schema content, extension namespace ownership, and extension
// semantics remain outside validation.
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
