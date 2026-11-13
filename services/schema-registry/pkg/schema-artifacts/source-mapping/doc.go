// Package sourcemapping parses source mapping descriptors and validates their schema references and opaque program
// bindings. A source mapping descriptor names an input schema, an optional condition, and an ordered, non-empty list
// of output transformations. Each program reference binds a named resource owned by the mapping artifact to its exact
// digest.
//
// # Parsing and validation
//
// [Parse] checks descriptor shape, supported format version, lexical selectors, engine metadata, and transformation
// cardinalities. It neither resolves schema references nor verifies resources.
//
// [Validate] additionally checks descriptor bytes, resource integrity, program ownership, dependencies, and schema
// selection agreement. [ValidateParsed] skips parsing when a caller already holds a descriptor corresponding to the
// artifact's exact payload bytes. [ValidateRelease] applies these checks to multiple distinct mapping identities while
// allowing shared input schemas.
//
// # Schema release context
//
// Callers MUST supply a trusted lineage through [ValidationContext], starting with the containing release and then its
// ancestors. A historical source selector MUST resolve to the same revision selected in the containing release.
// Output targets MUST be canonical schemas selected unchanged in both the declared target release and the containing
// release. [ValidationContext.ReusedMappings] optionally supplies previously validated mappings for immutable-revision
// comparison. These functions do not discover history or retrieve missing schemas and resources.
//
// # Program boundary
//
// [Engine] declarations are publisher metadata, not registered execution capabilities.
// Validation never compiles, interprets, or executes program bytes, and never certifies their runtime behavior.
// Results preserve transformation order and derive output classes from resolved target schemas.
// Full artifact identity checks and release-wide closure validation remain separate responsibilities of the
// 'artifact' and 'manifest' subpackages.
package sourcemapping
