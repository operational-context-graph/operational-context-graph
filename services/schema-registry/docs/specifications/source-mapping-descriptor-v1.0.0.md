# Source Mapping Descriptor Specification v1.0.0

This document contains the specification for the source mapping descriptor used to describe source mapping artifacts
in the _Schema Registry_ of the _Operational Context Graph (OCG)_.

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

The source mapping format can accommodate different execution engines and languages for defining the mapping logic. This
allows the transformation logic to evolve over time. A full source mapping revision contains the following files:

- The source mapping descriptor, which contains metadata and schema references as the primary artifact payload.
- An optional condition program, stored as an additional resource of the same artifact revision.
- One or more transformation programs, stored as additional resources of the same artifact revision.

The _source mapping descriptor_ is a JSON document defining the metadata of the mapping. The media type for a source
mapping descriptor is `application/vnd.apeiro.ocg.source-mapping.v1+json`.

The JSON document consists of one top-level object with these fields:

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `$schema` | string | Yes | Absolute immutable URI for the descriptor format schema. |
| `formatVersion` | string | Yes | `1.0.0`. |
| `id` | string | Yes | Mapping logical type; equals the release manifest artifact type. |
| `sourceSchema` | object | Yes | Source schema logical identity and selector. |
| `engine` | object | Yes | Language, version, and execution profile shared by every program. |
| `programs` | object | Yes | Optional `condition` and non-empty ordered `transformations`. |

Each program is a separate file in its language-specific format. The `programs` object describes the relevant metadata
of each program:

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `condition` | object | No | Program reference and digest; omission preserves absence. `null` is invalid. |
| `transformations` | array | Yes | One or more transformation objects in descriptor order. |

Each condition or transformation entry contains:

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `programRef` | string | Yes | Exact additional resource name within this revision. |
| `programDigest` | string | Yes | SHA-256 digest of that resource's exact bytes. |
| `targetSchema` | object | For transformation | Canonical target schema identity and selector. |
| `cardinality` | string | For transformation | `one` or `many` for that transformation. |

Condition entries reject transformation-only fields. Every required object rejects `null` or a different JSON type.

A schema reference (used for source and target JSON schemas) contains an `id` and exactly one selector:

| Field | Type | Rule |
| --- | --- | --- |
| `id` | string | Logical schema type. |
| `revision` | string | Exact artifact revision selector. |
| `version` | string | Schema-release selector resolved to an exact revision. |

`revision` and `version` are mutually exclusive. The resolved source and every target revision occur in the source
mapping's dependencies.

A revision selector equals its dependency revision in the schema release manifest. A version selector resolves through
that release's selected schema revision.

A reused source mapping revision preserves all target dependency revisions. It also passes compatibility validation
against the containing release.

Each transformation program defines how much output records it produces when executed, as indicated by its `cardinality`
property. Cardinality `one` means a transformation produces one canonical record when it is executed. Cardinality `many`
allows multiple records per transformation. A skipped mapping produces no records regardless of cardinality.

The engine object has these fields:

| Field | Type | Rule |
| --- | --- | --- |
| `language` | string | Non-empty publisher-declared language identifier. |
| `languageVersion` | string | Publisher-declared semantic language version. |
| `executionProfile` | string | Non-empty publisher-declared execution-profile identifier. |

All format-defined objects reject unknown fields.

### Complete example

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

### Cross-validation rules

- The source mapping descriptor `id` MUST equal the artifact type in the artifact descriptor for the source mapping
  in the schema release manifest.
- Every reference to a source and target schema MUST declare a dependency in the schema release manifest. Repeated
  target schemas in a source mapping descriptor only create one dependency for the target schema.
- One source schema revision can have zero, one, or several candidate source mappings within a schema release. Shared
  source schemas and overlapping conditions are valid for source mapping descriptors.
- Every `programRef` in a source mapping descriptor MUST resolve to exactly one resource in the schema release
  manifest with role `program` in the same revision.
- Every `programDigest`, resource digest, size, and byte value MUST match corresponding values of the resource
  descriptor in the schema release manifest a program is referencing.
- Every program resource in a release MUST be referenced by a source mapping descriptor. Repeated references preserve
  all transformation entries without duplicating resource descriptors or bytes.
- Each transformation program's `targetSchema.version`, when present, MUST equal the `targetSchemaVersion` in the
  release manifest artifact defined for the source mapping. That version identifies the containing release or one
  ancestor and selects every resolved transformation target.
