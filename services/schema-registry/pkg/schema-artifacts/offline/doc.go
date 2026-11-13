// Package offline supplies deny-by-default document loaders for JSON Schema and JSON-LD validation.
//
// Loaders either reject every lookup or resolve exact identifiers through a caller-supplied in-memory catalog.
// They never retrieve missing documents from the network, filesystem, or environment.
//
// Catalog values are decoded JSON documents, not serialized bytes. Loaders return those values without copying or
// validating them. Callers MUST validate catalog contents and keep the catalog and its documents stable while
// consumers use them. Identifiers are exact map keys; the loaders do not normalize identifiers or resolve aliases.
//
// [NewJSONSchemaCompiler] and [NewCatalogJSONSchemaCompiler] configure these lookup policies on a JSON Schema compiler.
// Locally registered resources and the compiler's built-in schemas remain available independently of loader lookups.
//
// [ValidateInputSize] provides a byte-length check only; it does not parse a document.
package offline
