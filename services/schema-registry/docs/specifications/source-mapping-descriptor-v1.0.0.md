# Source Mapping Descriptor Specification v1.0.0

This document specifies source mapping descriptors for the _Schema Registry_ of the _Operational Context Graph (OCG)_.

## Source mapping descriptor format

A _source mapping_ describes how to transform source records into canonical OCG records. JSON schemas define both record
formats. One source record can produce records of different canonical types. For example, one mapping can produce an
entity record and edge records describing its relationships.

Several source mappings can accept the same source schema revision within one release. Each mapping can define a
condition that determines whether it applies to a source record. For example, the NetBox `/api/dcim/devices/` endpoint
returns records for different physical and logical devices. The source record's `device_type` property distinguishes
device types. The canonical model uses explicit entity types such as `Chassis`, `NetworkDevice`, and `LoadBalancer`,
rather than one generic `Device` type. Separate mappings accept the same source schema but check different `device_type`
values before producing their canonical records.

The source mapping format can accommodate different execution engines and languages for defining the mapping logic.
A source mapping revision contains:

- The source mapping descriptor, which contains metadata and schema references as the primary artifact payload.
- An optional condition program, stored as an additional [resource](schema-release-manifest-v1.0.0.md#byte-descriptors)
  with role `program` in the same artifact revision.
- One or more transformation programs, stored as additional program resources in the same artifact revision.

Several program entries MAY reference the same resource. Resource names MUST remain unique within the artifact revision.
A revision MAY contain ancillary resources with other roles. Those resources do not require program references.

The _source mapping descriptor_ is a JSON document defining the metadata of the source mapping. The media type for a
source mapping descriptor is `application/vnd.apeiro.ocg.source-mapping.v1+json`.

The following sections define version `1.0.0` of the descriptor format. A publisher MUST reject a candidate descriptor
that does not satisfy this specification. The _Schema Registry Release Importer_ validates the descriptor and its
release dependencies before storing release data.

### Top-level object

The top-level object contains the source mapping metadata, source schema reference, execution engine declaration, and
program references.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `$schema` | string | Yes | Absolute immutable URI for the descriptor format schema. |
| `formatVersion` | string | Yes | `1.0.0`. |
| `id` | string | Yes | Source mapping logical artifact type; equals the release manifest entry's `artifactType`. |
| `sourceSchema` | object | Yes | Source schema logical identity and selector. |
| `engine` | object | Yes | Language, version, and execution profile shared by every program. |
| `programs` | object | Yes | Optional `condition` and non-empty ordered `transformations`. |

All fields are explicit. The top-level object and all format-defined nested objects MUST NOT contain unknown fields.
Every required field MUST be present with the specified JSON type. Required objects MUST NOT be `null`.

The descriptor MUST conform to the Internet JSON (I-JSON) constraints in [RFC 7493](https://www.rfc-editor.org/rfc/rfc7493).

Every string field MUST be non-empty unless its field definition says otherwise.

UTF-8 is the only allowed byte encoding for the source mapping descriptor.

Parsers MUST reject duplicate object member names before semantic validation, including names repeated within nested
objects.  Parsers MUST reject malformed JSON, invalid UTF-8, and other I-JSON violations.

`$schema` MUST be an absolute, immutable Uniform Resource Identifier (URI). It MUST identify a JSON Schema that
implements the declared `formatVersion`. `formatVersion` follows semantic versioning. Consumers MUST reject an
unsupported major format version. They MAY accept a newer minor or patch version only when they implement every added
field and rule.

### Programs

The `programs` object describes the optional condition and ordered transformations:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `condition` | object | No | Program reference and digest; omission preserves absence. `null` is invalid. |
| `transformations` | array | Yes | One or more transformation objects in descriptor order. |

The descriptor MUST contain a non-empty `programs.transformations` array and MAY contain `programs.condition`.
Omitting `condition` preserves its absence. A present condition MUST be an object, not `null`.

Each condition or transformation entry contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `programRef` | string | Yes | Exact additional resource name within this revision. |
| `programDigest` | string | Yes | SHA-256 digest of that resource's exact bytes. |
| `targetSchema` | object | For transformation | Canonical target schema identity and selector. |
| `cardinality` | string | For transformation | `one` or `many` for that transformation. |

Each transformation MUST contain `targetSchema` and `cardinality` in addition to its program reference and digest.
The condition MUST NOT contain transformation-only fields.

Every program entry MUST resolve `programRef` to exactly one resource's `name` within the same artifact revision.
That resource MUST have role `program`, and its digest MUST equal the entry's `programDigest`. Every resource with
role `program` MUST be referenced by the descriptor. Repeated references to the same resource do not duplicate that
resource.

Resource names identify resources within an artifact revision. They are release manifest-local identifiers, not paths
resolved against the retrieval URI. Consumers MUST compare resource names exactly without path normalization.

### Schema references

A schema reference (used for source and target JSON schemas) contains an `id` and exactly one selector:

| Field | JSON type | Description |
| --- | --- | --- |
| `id` | string | Logical artifact type of the schema. |
| `revision` | string | Exact artifact revision selector. |
| `version` | string | Schema-release selector resolved to an exact revision. |

`revision` and `version` are mutually exclusive. Each reference MUST contain exactly one non-empty selector.
The source schema and every transformation target schema MUST resolve to dependency entries with matching logical
artifact types and revisions. A revision-based descriptor reference MUST equal the dependency revision. A version-based
reference resolves through the named release's selected schema revision.

#### Source schema

A source revision selector MUST identify the exact schema revision selected by the containing release.
`sourceSchema.version` MUST identify the containing release or one of its ancestors. The named release and containing
release MUST select the same revision for the source schema logical artifact type. Validation MUST reject descendant
or unrelated source versions and source revisions absent from the containing release.

#### Target schemas

Every transformation program's target schema MUST be a canonical schema selected by both
[`targetSchemaVersion`](schema-release-manifest-v1.0.0.md#artifact-entries) and the containing release. Both releases
MUST select the same target revision. `targetSchemaVersion` is the field of the source mapping's artifact in the schema
release manifest, not a descriptor field. It MUST identify the containing release or one of its ancestors and apply to
every transformation program.

A version-based target reference MUST equal `targetSchemaVersion`. A revision-based target reference MUST equal the
schema revision selected by `targetSchemaVersion`. Its resolved schema revision MUST occur in the `dependencies` of
the source mapping artifact in the containing release. Validation MUST reject descendant or unrelated target versions
and mismatched target revisions.

The target schema's `schemaClass` defines the transformation's output classification. Canonical target classes are
`entity-type`, `identifier-type`, and `edge-type`. A transformation program MUST NOT target a `source-record` schema.

### Transformation cardinality

Each transformation program MUST declare `cardinality` as `one` or `many`. Cardinality `one` means a transformation
program produces one canonical record when executed. Cardinality `many` allows multiple records per transformation
program. A skipped mapping produces no records regardless of cardinality.

### Execution engine

The engine object has these fields:

| Field | JSON type | Description |
| --- | --- | --- |
| `language` | string | Non-empty publisher-declared language identifier. |
| `languageVersion` | string | Publisher-declared semantic language version. |
| `executionProfile` | string | Non-empty publisher-declared execution-profile identifier. |

Every engine field MUST be present and non-empty. The engine declaration MUST apply to the condition and every
transformation program. Language and execution-profile identifiers are publisher-declared metadata, not registered
_Schema Registry_ execution capabilities. The _Schema Registry_ preserves unknown language and execution-profile
identifiers without inferring runtime support. A consumer that does not implement the declared engine MUST treat
the source mapping as unsupported.

### Program validation boundary

Each program resource MUST declare a syntactically valid, non-empty media type. The media type is publisher-declared
metadata, not a registry-selected program parser. Program bytes MAY contain arbitrary binary data. Empty bytes
are valid when size and digest agree. Every program digest and size MUST match the exact uncompressed bytes.

The _Schema Registry_ validates resource identity, role, media type syntax, size, digest, bytes, and descriptor binding.
It doesn't parse, compile, execute, or otherwise interpret condition or transformation programs. It doesn't evaluate
conditions, validate program behavior, or certify execution support.

Program syntax, semantics, and execution failure handling remain outside this specification.

### Revision immutability and dependencies

The descriptor and all referenced programs form one immutable source mapping revision. Program resources do not create
separate logical artifacts. Changing a condition, transformation, transformation order, target reference, or program
bytes creates a new source mapping revision.

The [Schema Release Manifest specification](schema-release-manifest-v1.0.0.md#artifact-entries) defines all
revision-bound metadata. These values MUST remain identical wherever a release reuses the revision.
They include descriptor bytes, engine metadata, every resource, dependencies, and `targetSchemaVersion`.

The `bundlePath` in a release manifest artifact descriptor is release packaging metadata. A publisher MAY change it
without creating a new revision, but it MUST identify the bound bytes.

A source mapping can be reused only when all target dependencies retain their revisions and current-release compatibility
validation succeeds.

Publishers MUST declare dependencies for a source mapping artifact in the schema release manifest for every external
reference that affects validation, expansion, or data transformation. Every dependency target MUST occur in the release
`artifacts` with the same kind, logical type, and revision. Condition and transformation program dependencies also
belong to the release closure. Repeated references to one target schema MUST use one dependency entry. Internal
references within one payload do not create dependency entries.

Validators MUST verify those declarations against the release closure without inspecting or executing program bytes.
A publisher MUST reject hidden network dependencies that are neither selected artifacts nor additional resources.

The release manifest specification's [artifact dependency rules](schema-release-manifest-v1.0.0.md#artifact-dependencies)
apply to source mappings.

### Candidate source mappings

One source schema revision can have zero, one, or several candidate source mappings within a schema release.
Each source mapping MUST retain its own unique logical artifact type, which MAY differ from the source schema's type.
Publishers and importers MUST NOT reject mappings solely because they share a source schema or have overlapping
conditions.

### Ordering and exact bytes

The descriptor does not require the JSON Canonicalization Scheme (JCS) serialization used by the release manifest.
Whitespace and object member ordering MAY differ between descriptor serializations.

Any SHA-256 digest of the descriptor MUST cover its exact UTF-8 bytes, not a reserialized JSON value.
Consumers MUST preserve transformation array order, including repeated program and target references.
Manifest resource and dependency sorting MUST NOT reorder or deduplicate descriptor transformations.

### Digest and identifier syntax

Source mapping descriptor metadata uses these common lexical forms:

| Value | Required form |
| --- | --- |
| SHA-256 digest | `sha-256:` followed by 64 lowercase hexadecimal characters |
| Schema or language version | `0.0.0` or a higher `MAJOR.MINOR.PATCH` value without leading zeroes |
| Artifact revision | One or more RFC 3986 unreserved characters; values `.` and `..` are forbidden |
| Logical artifact type | `[a-z0-9]+(?:-[a-z0-9]+)*` |

Source mapping `id` and schema-reference `id` fields MUST follow the logical artifact type syntax.

Artifact revisions are opaque to consumers. Consumers MUST NOT derive time or ordering from their representation.
`version` selectors and `languageVersion` MUST contain only `MAJOR.MINOR.PATCH`. Prerelease identifiers and build
metadata are not allowed.

The `sha-256:` spelling applies to Schema Registry JSON metadata. It intentionally differs from the `sha256:` spelling
used by Open Container Initiative (OCI) descriptors.

### Cross-field validation

The publisher and importer enforce these invariants after structural validation:

- The descriptor `id` equals the source mapping artifact entry's `artifactType`.
- The source schema and every transformation program target schema resolve to exact schema dependencies selected by the
  containing release.
- Every source schema version identifies the containing release or an ancestor selecting the same source revision.
- Every target schema matches the canonical schema revision selected by `targetSchemaVersion` and the containing release.
- Every condition and transformation program reference matches a resource name, role, and digest in its source mapping
  revision.
- Every digest matches the bytes and every size matches the byte length before publication succeeds.
- Every resource with role `program` is referenced by a source mapping descriptor.
- Every external runtime reference is represented by an artifact, resource, dependency, or URI catalog entry.
- Reused revisions preserve every revision-bound value and pass current-release validation.

### Complete example

The following example illustrates the complete structure. Example engine identifiers and file extensions do not define
registry-supported execution capabilities. Program digests are illustrative references, not integrity test vectors
without the corresponding resource bytes.

```json
{
  "$schema": "https://schemas.example.org/ocg/source-mapping-artifact-1.0.json",
  "formatVersion": "1.0.0",
  "id": "example-cmdb-device",
  "sourceSchema": {"id": "example-cmdb-device", "revision": "01K4Q80PQ2J6R0A7KX9N3FHTVE"},
  "engine": {
    "language": "ocg-dsl",
    "languageVersion": "1.0.0",
    "executionProfile": "ocg-custom-transformations"
  },
  "programs": {
    "condition": {
      "programRef": "example-cmdb-device-condition.ocgdsl",
      "programDigest": "sha-256:2ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3"
    },
    "transformations": [
      {
        "cardinality": "one",
        "targetSchema": {"id": "device", "version": "2.4.0"},
        "programRef": "example-cmdb-device-transformation.json",
        "programDigest": "sha-256:1ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3"
      },
      {
        "cardinality": "one",
        "targetSchema": {"id": "located-in", "version": "2.4.0"},
        "programRef": "example-cmdb-device-location.json",
        "programDigest": "sha-256:3ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3"
      }
    ]
  }
}
```
