# Schema Release Manifest Test Fixtures

This directory is the permanent corpus for artifact, source mapping, release manifest, and release-serviceability
validation. Tests read only these checked-in fixtures.

## Fixture sources

- `corpus.json` indexes exact-byte and structural manifest vectors, including intentionally invalid encodings.
- `release-set.json` defines complete byte-backed release snapshots and predecessor relationships.
- `mapping-*.json` contains complete descriptor payloads. Their exact bytes include their final newline.
- `release-failures.json` defines isolated invalid fixtures as mutations of valid release snapshots.

Resource `content` uses padded Base64. Empty content represents zero bytes, not missing bytes.

The fixture builder  computes descriptor sizes, digests, bundle paths, and canonical manifests from these sources.
It resolves inherited  selections into complete snapshots before validation. It performs no storage writes or
activation. Publisher-declared program digests remain independent constants in each descriptor, so incorrect bindings
fail validation.

## Validation phases and diagnostics

The release corpus executes these observable boundaries in order:

1. `parse`: bounded strict JSON, field shapes, lexical forms, canonical bytes, and array ordering.
2. `closure`: immutable metadata, exact bytes, offline payload standards, references, mapping bindings, lineage, and deterministic changes.
3. `serviceability`: complete generated package sizing, including metadata, escaping, and Base64 expansion.

These are API boundaries, not claims that each internal validator implements a separate pipeline stage.
For example, descriptor JSON is checked during release closure validation, after manifest parsing succeeds.

Every invalid release recipe records a stable ID, rule, phase, sentinel classification, and diagnostic substring.
The test runner first verifies the unmodified snapshot passes every boundary. It requires failure at the declared
boundary and checks the error sentinel chain. It repeats validation and requires the complete diagnostic to remain
identical. Unknown mutation operations and unclassified errors fail tests. Large boundary fixtures expand
deterministically in memory rather than adding large binary files to Git.

## Rule traceability

Each rule below has invalid recipes in `release-failures.json` and a valid baseline in `release-set.json`.
The test runner checks both directions of this index to reject missing rules and misspelled references.

Requirement names identify the format contracts independently of the OpenSpec specifications.

| Rule | Normative requirement or constraint |
| --------------------------------| ----------------------------------------------------------------------------------------------- |
| `artifact.additional-resource` | Additional resource descriptor: unique safe names, roles, and byte metadata. |
| `artifact.canonical-uri` | Canonical artifact URI: configured origin and exact immutable path coordinates. |
| `artifact.dependencies` | Artifact dependencies: exact selected closure, uniqueness, and offline references. |
| `artifact.digest-size` | Content digest and size: exact bytes and SHA-256 spelling. |
| `artifact.immutable-revision` | Immutable artifact revision: reuse preserves metadata, programs, and extensions. |
| `artifact.json-ld-context` | JSON-LD context artifact format and profiled media type. |
| `artifact.json-schema` | JSON Schema artifact format: Draft 2020-12 object or boolean documents. |
| `artifact.logical-identity` | Logical artifact identity: case-sensitive registered lexical form. |
| `artifact.payload-descriptor` | Primary payload descriptor: required fields and safe relative bundle paths. |
| `artifact.registered-kinds` | Registered artifact kinds and schema classifications. |
| `artifact.strict-json` | Strict JSON artifact parsing: duplicate-member and encoding rejection. |
| `manifest.array-ordering` | Deterministic array ordering with UTF-16 code-unit comparisons. |
| `manifest.artifact-changes` | Artifact change entries: operation-specific before and after revision fields. |
| `manifest.artifact-selection` | Artifact selection entries: unique logical selections and conditional fields. |
| `manifest.canonical-bytes` | Canonical manifest bytes: no byte-order mark, newline, or self-digest. |
| `manifest.change-summary` | Optional deterministic change summary: exact predecessor difference. |
| `manifest.compatibility` | Conservative compatibility classification and exact version increment. |
| `manifest.context-bindings` | Schema-context bindings: non-empty exact revisions of the required kinds. |
| `manifest.cross-field` | Complete cross-field validation: exact selected metadata and available bytes. |
| `manifest.extensions` | I-JSON and extension constraints: namespaced, non-semantic metadata. |
| `manifest.format` | Manifest media type and format declaration. |
| `manifest.i-json` | I-JSON encoding, duplicate-member rejection, and interoperable numbers. |
| `manifest.lexical-forms` | Manifest lexical forms, including published schema-version syntax. |
| `manifest.multi-program-closure` | Multi-program source mapping closure: every target has storage. |
| `manifest.pair-changes` | Binding, URI, and storage change entries: registered operations and impacts. |
| `manifest.predecessor` | Previous release binding: exact immediate predecessor version and digest. |
| `manifest.provenance` | Release source provenance: absolute repository URI and full lowercase Git identifier. |
| `manifest.registered-stores` | Registered store identifiers: logical names only, without deployment details. |
| `manifest.revision-reuse` | Creation version and revision reuse: earliest selecting version remains bound. |
| `manifest.storage-catalog` | Storage catalog: canonical schemas require placement; source records forbid placement. |
| `manifest.top-level` | Manifest top-level object: all required fields and non-empty selections. |
| `manifest.unsharded-closure` | Complete unsharded runtime closure: references instead of embedded bytes. |
| `manifest.uri-catalog` | URI and IRI catalog: exact keys, selected targets, and owned resources. |
| `mapping.cardinality` | Transformation cardinality: only one or many, independently for each transformation. |
| `mapping.composition` | Source mapping revision composition: descriptor media type and opaque program media types. |
| `mapping.dependency-agreement` | Parsed schema-reference agreement: source and every distinct target dependency. |
| `mapping.engine` | Execution engine descriptor: required publisher metadata and semantic language version. |
| `mapping.format` | Descriptor format version: supported version and immutable absolute schema URI. |
| `mapping.identifier` | Mapping identifier: descriptor identity equals the mapping's own logical type. |
| `mapping.output-classification` | Target schema defines output classification; outputKind is forbidden. |
| `mapping.program-binding` | Program reference and digest binding: exact name, role, digest, size, and bytes. |
| `mapping.programs` | Programs object and entries: optional non-null condition and non-empty ordered transformations. |
| `mapping.resource-set` | Referenced program resource set: no unreferenced program resources. |
| `mapping.source-reference` | Source schema reference: exactly one selector, selected dependency, and valid lineage. |
| `mapping.target-lineage` | Target schema version lineage: containing release or ancestor. |
| `mapping.target-reference` | Target schema reference: selected canonical revision and target-version agreement. |
| `mapping.top-level` | Descriptor top-level fields: required objects and no legacy single-program fields. |
| `profile.entries` | Bounded manifest and artifact profile: 10000 entries per array, including transformations. |
| `profile.exact-bytes` | Bounded manifest and artifact profile: 16777216 exact bytes per manifest, payload, or resource. |
| `profile.generated-response` | Bounded generated representations: complete package identity JSON fits 67108864 bytes. |
| `profile.identifiers` | Bounded artifact profile: identifiers fit 255 UTF-8 bytes. |
| `profile.media-types` | Bounded artifact profile: media types fit 255 UTF-8 bytes. |
| `profile.members` | Bounded JSON profile: 256 members per object. |
| `profile.nesting` | Bounded JSON profile: 64 nesting levels. |
| `profile.strings` | Bounded JSON profile: 1048576 UTF-8 bytes per string. |
| `profile.uris` | Bounded artifact profile: 8192 UTF-8 bytes per URI or IRI. |
