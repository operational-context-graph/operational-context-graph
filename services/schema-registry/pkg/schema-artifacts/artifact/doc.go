// Package artifact models immutable Schema Registry artifact revisions and verifies their exact byte representations.
// Supported kinds are JSON Schema, JSON-LD context, and source mapping descriptor artifacts.
//
// A [Descriptor] binds an uncompressed representation's media type, byte count, SHA-256 digest, and offline bundle
// path. [Artifact] and [Resource] values pair those descriptors with bytes supplied by the caller. Bundle paths are
// package-relative identifiers, not authorization to read the host filesystem.
//
// [Artifact.Validate] checks identity, kind-specific metadata, canonical resource location, and descriptor integrity.
// [Artifact.ValidatePayloadFormat] and [Artifact.ValidatePayloadFormatWithCatalog] separately check JSON syntax and
// schema or context formats. Schema payloads require JSON Schema Draft 2020-12. Catalog-based validation resolves
// supplied documents without network or filesystem fallback.
//
// Source-mapping descriptor semantics and program bindings are provided in the 'sourcemapping' subpackage. Release-wide
// dependency resolution, historical immutability, and catalog agreement are provided in the 'manifest' subpackage.
//
// Constructing an [Artifact] does not validate it or make its slices and maps immutable. Callers MUST keep metadata
// and bytes consistent while validating or sharing a revision.
package artifact
