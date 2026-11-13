// Package strictjson parses bounded RFC 7493 Internet JSON (I-JSON) documents for Schema Registry artifacts.
// It rejects duplicate object members, invalid UTF-8 input, oversized values, and unsupported numeric ranges.
// Shared limits come from the parent [schemaartifacts] package.
//
// [Parse] consumes exactly one JSON value and retains numbers as [json.Number] rather than converting them to float64.
//
// [Object] additionally requires an object at the document root.
//
// [RejectUnknownFields] checks an object's allowed member names without enforcing required fields or value types.
//
// Callers remain responsible for format-specific metadata, canonical representation, and reference validation.
//
// This package does not canonicalize JSON or require JSON Canonicalization Scheme (JCS) input.
// String escape decoding follows [encoding/json]; JCS callers MUST separately reject invalid Unicode surrogate escapes.
// The manifest subpackage performs that additional check during canonicalization and parsing.
//
// Callers MAY classify failures with [errors.Is] and the exported error sentinels.
// Diagnostic text provides context but is not a machine-readable validation contract.
package strictjson
