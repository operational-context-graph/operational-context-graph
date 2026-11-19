// Package strictjson parses bounded RFC 7493 Internet JSON (I-JSON) documents for Schema Registry artifacts.
// It rejects duplicate object members, invalid Unicode, oversized values, and unsupported numeric ranges.
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
//
// Unicode validation rejects malformed UTF-8, unpaired surrogate escapes, and Unicode noncharacters before string
// decoding. Valid surrogate pairs and literal or escaped replacement characters retain their values. Unpaired surrogate
// escapes and noncharacters match [ErrUnicode] and [ErrMalformed]. Malformed UTF-8 matches [ErrMalformed].
//
// Callers MAY classify failures with [errors.Is] and the exported error sentinels.
// Diagnostic text provides context but is not a machine-readable validation contract.
package strictjson
