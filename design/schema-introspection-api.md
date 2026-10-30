# Schema Registry

The Schema Registry is the canonical source of the data schema of the Operational Context Graph (OCG). It stores and
versions every _entity type, identifier type, edge type, and source mapping as immutable schema artifacts. Data Ingestion
and the Semantic Network Builder read it before they write, so all OCG data shares one governed vocabulary.

Schema artifacts and releases are authored and versioned in a dedicated Git-based environment. The Schema Registry
imports published schema releases from the Collaborative Version Control System (e.g. GitHub) used for this schema
authoring environment.

## Related ADRs and Documents

- Specification: [Schema Registry](/Users/D070575/Source/apeiro-autonomous-operations-internal/docs/content/Architecture/Specifications/spec-ocg/components/schema-registry/index.md)
- ADR: [Schema Authoring Environment](/Users/D070575/Source/apeiro-autonomous-operations-internal/docs/content/Architecture/Specifications/spec-ocg/adrs/adr-012/index.md)
- ADR: [Schema Introspection API](/Users/D070575/Source/apeiro-autonomous-operations-internal/docs/content/Architecture/Specifications/spec-ocg/adrs/adr-015/index.md)
- ADR: [Source Mappings](/Users/D070575/Source/apeiro-autonomous-operations-internal/docs/content/Architecture/Specifications/spec-ocg/adrs/adr-019/index.md)

## Versioning model

The Schema Registry MUST support four distinct notions of versioning.

| Identifier | Example | Meaning |
| --- | --- | --- |
| API version | `v1` | Version of the HTTP contract |
| Schema version | `2.4.0` | Semantic version of one complete data model release |
| Artifact revision | `01K4...` | Opaque immutable revision of one artifact |
| Content digest | `sha-256:...` | Digest of the exact stored artifact bytes |

The _schema version_ is the global version that consumers use to detect data model advancement. It follows semantic
versioning as stated above. Every published change creates a new schema version. This includes a context-only change
or an operational rollback. A rollback restores earlier artifact revisions in a new release. Its `MAJOR`, `MINOR`, or
`PATCH` increment follows the effective compatibility change from the current release.

- A schema version MUST resolve to one immutable release manifest. That manifest MUST identify every artifact, association,
  and controlled dependency in the release.
- The Schema Registry MUST never assign the same schema version to different manifests.
- The Schema Registry MUST never change bytes behind an artifact revision or digest.

The _artifact revision_ binds an artifact kind, logical artifact type, schema classification, media type, canonical
identifier, and exact bytes. Format information is part of the artifact bytes. A change to any bound value creates a new
revision. Several schema releases can reuse one revision. Clients SHOULD treat the revision value as opaque.

The _content digest_ verifies bytes and supports content-addressed storage. JSON metadata uses `sha-256:` followed by 64
lowercase hexadecimal characters. A digest does not record approval, publication time, or compatibility intent.

### Artifact identifiers

A _logical artifact_ is a named data-model definition that continues to exist across releases. The logical artifact is
not one stored file or one immutable revision. It represents the concept whose definition changes over time.

A _logical artifact type_ is the stable registry name for that logical artifact. Consumers use it to ask questions
such as:

- What is the `device` entity schema in release `2.4.0`?
- Which revisions of the `location` entity schema have existed?
- Which JSON-LD context is associated with the `device` schema?
- Which schemas refer to the `depends-on` edge schema?

All schema artifacts MUST have a logical artifact type.

The logical artifact type does not change when the schema artifact content changes. For example, the `device` schema
can have revision `01K4...` in release `2.4.0` and revision `01K8...` in release `2.5.0`. Both revisions belong to the
logical artifact named `device`.

A logical artifact type MUST be unique within an artifact kind. The complete logical identity is the pair
`(artifact kind, logical type)`. For example, `(schema, device)` and `(context, device)` are different logical
artifacts. The first defines the valid JSON shape. The second maps properties to external vocabularies.

This distinction produces three levels of identity:

| Identity | Example | Stability |
| --- | --- | --- |
| Logical artifact type | `(schema, device)` | Stable for the lifetime of the concept |
| Artifact revision | `01K4Q7S2YV4B5MC7B8G8DCDR5F` | Immutable version of the artifact |
| Release selection | `2.4.0` selects revision `01K4...` | Mapping fixed by one release manifest |

- The Schema Registry SHOULD use lowercase ASCII logical artifact types that consist only of alphanumeric characters,
  i.e., match the regular expression `[a-z0-9]+(?:-[a-z0-9]+)*`. Types are case-sensitive and require no path encoding.
- The Schema Registry MUST not reuse a logical artifact type after retirement. Reuse could cause historical references
  to appear to refer to a different concept.
- Each object-form JSON Schema resource SHOULD also have an absolute `$id`. The preferred `$id` resolves to an immutable
  artifact revision. Reusing one `$id` or changing content makes `$ref` resolution depend on hidden state.

## Schema artifacts in the registry

The Schema Registry stores a number of schema artifact kinds. Together, they define the canonical data model used by the
OCG and explain how source data relates to that model.

- _JSON schemas_ define the structure and constraints of data models.
- _JSON-LD contexts_ map canonical properties to external vocabulary IRIs.
- _Source mappings_ transform source records into canonical records.

All schema artifact kinds are immutable and versioned. A _Schema Release Manifest_ selects the exact revision of each
schema artifact that belongs to one data schema version.

### Schemas

A schema is a [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12) document. It defines the allowed shape,
properties, value types, and validation constraints for data. JSON schemas are used to describe both source-specific data
structures and the canonical data model of the OCG.

### JSON-LD contexts

A [JSON-LD](https://json-ld.org/) context assigns semantic identifiers to properties in canonical JSON data. It can map
a short property such as `name` to an Internationalized Resource Identifier (IRI) from an external vocabulary.

An illustrative context can contain:

```json
{
  "@context": {
    "name": "https://schema.org/name",
    "manufacturer": "https://schema.org/manufacturer"
  }
}
```

### Source mappings

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

The source mapping descriptor is a JSON document defining the metadata of the mapping:

- `formatVersion`: The version of the source mapping descriptor format.
- `id`: The logical artifact type of the source mapping, independent of its source schema's logical artifact type.
- `sourceSchema`: The source JSON Schema reference, using `id` and either `revision` or `version`.
- `engine`: The language, language version, and execution profile shared by the condition and transformation programs.
  The execution profile defines allowed custom functions.
- `programs`: The optional `condition` entry and the non-empty `transformations` array.

Each program entry contains `programRef` and `programDigest`. These fields identify an additional resource and verify its
exact bytes. Each transformation also contains its own `targetSchema` reference, using `id` and either `revision` or
`version`. There is no top-level `targetSchema`, `programRef`, or `programDigest`.

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

Each program is a separate file in its language-specific format. This example produces a `device` entity and a
`located-in` edge from one source record. The registry resolves each target independently. The illustrative `device`
schema is independent of the specialized device types in the NetBox example.

Each transformation program defines how much output records it produces when executed, as indicated by its `cardinality`
property. `cardinality: one` means a transformation produces one canonical record when it is executed. `cardinality: many`
allows multiple records per transformation. A skipped mapping produces no records regardless of cardinality.

The pair `(schema version, source artifact type)` identifies a deterministic set of candidate mappings, not one mapping.
A release can contain zero, one, or several candidates. The registry exposes the complete set without evaluating
conditions. Condition evaluation belongs to the source mapping execution engine, not the introspection API.

### Release manifests

The _Schema Release Manifest_ is the main consistency and synchronization resource for a schema release. It describes a
complete release without embedding every document.

The manifest is a JSON document. Version 1 uses the media type
`application/vnd.apeiro.ocg.schema-release-manifest.v1+json`. The format is independent from the storage technology and
the Schema Introspection API representation.

The following sections define version `1.0.0` of the manifest format. A publisher MUST reject a candidate manifest that
does not satisfy this specification. A registry importer MUST validate the manifest before it stores any release data.

#### Format principles

The format uses these principles:

- The manifest contains metadata and references. It does not embed artifact bytes.
- Every reference that affects runtime interpretation resolves to an immutable artifact revision or immutable resource.
- The manifest contains the complete runtime closure. A consumer does not need the authoring repository to interpret a
  release.
- Relationships use artifact revisions. Logical artifact types remain useful query keys, but they do not identify exact
  content.
- Catalogs use arrays of objects instead of JSON object member names. This supports explicit validation and stable
  ordering.
- Version 1 is one unsharded document. A future format can add a root manifest and immutable shards if measurements show
  that sharding is necessary.

The manifest MUST NOT contain its own digest because that would create a self-reference.

#### Top-level object

The top-level object contains all data needed to identify, retrieve, and compare one release.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `$schema` | string | Yes | Absolute URI of the JSON Schema for this manifest format |
| `formatVersion` | string | Yes | Manifest format version; `1.0.0` for this specification |
| `schemaVersion` | string | Yes | Semantic version of the complete OCG data schema release |
| `previousRelease` | object or `null` | Yes | Immediate predecessor, or `null` for the first release |
| `source` | object | Yes | Git snapshot from which the release was published |
| `artifacts` | array | Yes | Complete set of selected artifact revisions |
| `schemaContextBindings` | array | Yes | Contexts that apply to each schema revision |
| `uriCatalog` | array | Yes | URI and IRI resolution entries |
| `storageCatalog` | array | Yes | OCG stores for data governed by logical schemas |
| `changes` | object | No | Machine-readable changes and an optional textual release summary |
| `extensions` | object | No | Namespaced extension data that does not change core semantics |

All fields are explicit. The top-level object and all format-defined nested objects MUST NOT contain unknown fields.
Extension producers can add data under `extensions`. Each extension member name MUST be a reverse-domain name that the
producer controls, such as `org.example.review`. An extension MUST NOT change the meaning of a format-defined field.

The manifest MUST conform to the Internet JSON (I-JSON) constraints in
[RFC 7493](https://www.rfc-editor.org/rfc/rfc7493). Every string field MUST be non-empty unless its field definition says
otherwise. Every size MUST be an integer from `0` through `9007199254740991`.

`$schema` MUST be an absolute, immutable URI. It MUST identify a JSON Schema that implements the declared
`formatVersion`. `formatVersion` follows semantic versioning. Consumers MUST reject an unsupported major format version.
They MAY accept a newer minor or patch version only when they implement every field and rule introduced by that version.

`schemaVersion` MUST contain only `MAJOR.MINOR.PATCH`. Pre-release identifiers and build metadata are not allowed for
published releases. Candidate releases can use separate authoring metadata, but they are not published manifests.

#### Previous release

`previousRelease` binds the history chain to exact manifest bytes. A schema version alone is insufficient because it does
not detect an invalid reassignment of that version.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `schemaVersion` | string | Yes | Schema version of the immediate predecessor |
| `manifestDigest` | string | Yes | SHA-256 digest of the predecessor manifest bytes |

The first published release MUST use `null`. Every later release MUST identify the current release at publication time as
its immediate predecessor. A rollback still creates a new schema version and points to the release from which the
rollback occurs.

#### Release source

`source` identifies the authoritative Git snapshot from which the _Schema Authoring Environment_ published the release.
Review, approval, and pipeline evidence remains in that environment and is not duplicated in the manifest.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `repository` | string | Yes | Absolute URI of the Git repository |
| `commit` | string | Yes | Full lowercase hexadecimal Git object ID |

The commit MUST exist in the repository and contain the release source. The repository and commit provide the lookup key
for full provenance in the Schema Authoring environment.

#### Artifact entries

The non-empty `artifacts` array selects one revision for every logical artifact in the release. Version 1 defines the
registered kinds `schema`, `context`, and `source-mapping`.

Each artifact entry contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Registered artifact kind |
| `artifactType` | string | Yes | Logical artifact type within `kind` |
| `artifactRevision` | string | Yes | Opaque immutable revision identifier |
| `createdUnderSchemaVersion` | string | Yes | First schema release that selected this exact revision |
| `schemaClass` | string | For `schema` | Registered classification such as `entity-type` |
| `targetSchemaVersion` | string | For `source-mapping` | Canonical schema version that the artifact targets |
| `canonicalUri` | string | Yes | Absolute, immutable URI of this artifact revision |
| `payload` | object | Yes | Primary artifact byte descriptor |
| `resources` | array | Yes | Additional byte resources owned by the revision |
| `dependencies` | array | Yes | Statically discoverable artifact dependencies |
| `extensions` | object | No | Namespaced metadata that does not change core semantics |

`kind` and `artifactType` MUST match the logical identity bound to `artifactRevision`. `artifactType` MUST match
`[a-z0-9]+(?:-[a-z0-9]+)*`. `artifactRevision` is opaque to consumers. A registry can use a Universally Unique
Lexicographically Sortable Identifier (ULID), but consumers MUST NOT depend on that representation or derive time from it.

An artifact revision binds `kind`, `artifactType`, `schemaClass`, `targetSchemaVersion`, `canonicalUri`,
`createdUnderSchemaVersion`, payload media type and content, every resource name, role, media type and content, and all
dependencies. These values MUST remain identical wherever a release reuses the revision. `bundlePath` is release
packaging metadata. A publisher can change it without creating a new artifact revision, but it MUST identify the bound
bytes in the release bundle.

`createdUnderSchemaVersion` remains unchanged when a later release reuses the revision. It MUST equal the earliest release
in which the revision appears. `targetSchemaVersion` MUST identify this release or one of its ancestors. A source mapping
can be reused only when all its target dependencies select the same revisions and compatibility validation succeeds
against the current release. This rule permits safe reuse without hiding a change in the target model.

`schemaClass` is required only for `schema` entries. Values come from the Schema Registry's classification catalog. The
initial classifications include `entity-type`, `identifier-type`, `edge-type`, and `source-record`. Consumers MUST treat
unknown classifications as unsupported rather than guessing their semantics.

The primary payload of a `schema` entry MUST use `application/schema+json`. This requirement gives schema consumers one
unambiguous media type for validation documents.

`canonicalUri` MUST identify this exact revision. It MUST NOT be a `current` alias or another URI whose result can change.
Two artifact entries MUST NOT use the same canonical URI. The registry MUST reject a canonical URI that any historical
revision already uses for a different revision.

##### Byte descriptors

`payload` and every additional resource use content digests. A payload descriptor contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `mediaType` | string | Yes | Media type of the exact bytes |
| `digest` | string | Yes | SHA-256 digest of the exact bytes |
| `size` | integer | Yes | Number of bytes, greater than or equal to zero |
| `bundlePath` | string | Yes | Path of the same bytes in an offline release bundle |

Clients retrieve a primary payload through the artifact revision resource of the Schema Introspection API. They retrieve
an additional resource through that artifact revision and the resource name. `bundlePath` identifies the same bytes in an
offline release bundle. It MUST use `/` separators. It MUST NOT start with `/`, contain an empty segment, or contain `.`
or `..` segments. Every bundle path in a manifest MUST be unique.

An additional resource contains the same four byte descriptor fields and two more fields:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | Yes | Name unique within the artifact revision |
| `role` | string | Yes | Registered purpose of the resource |

A source mapping stores its descriptor as `payload`. Its condition and transformation programs are resources with role
`program`. The descriptor MUST contain a non-empty `programs.transformations` array and MAY contain `programs.condition`.
Every program entry MUST resolve `programRef` to exactly one resource's `name` within the same artifact revision. That
resource MUST have role `program`, and its digest MUST equal the entry's `programDigest`. Every resource with role
`program` MUST be referenced by the descriptor. Repeated references to the same resource do not duplicate that resource.

Resource names identify resources within an artifact revision. That is, they are manifest-local identifiers, not paths
resolved against the retrieval URI. The descriptor `id` MUST equal the artifact entry's `artifactType`. The descriptor's
source schema and every transformation target schema MUST resolve to dependency entries with matching logical identities
and revisions. A revision-based descriptor reference MUST equal the dependency revision. A version-based target reference
MUST equal `targetSchemaVersion`. Its resolved schema revision MUST occur in `dependencies`. Condition and transformation
program dependencies also belong to the release closure.

The descriptor and all referenced programs form one immutable source-mapping revision. Changing a condition, a
transformation, or a target reference creates a new revision. Program resources do not create separate logical artifacts.

Several source-mapping entries MAY resolve `sourceSchema` to the same schema logical artifact type and selected revision.
Each mapping MUST retain its own unique logical artifact type. Publishers and importers MUST NOT reject mappings solely
because they share a source schema or have overlapping conditions.

##### Artifact dependencies

`dependencies` makes cross-artifact references discoverable without parsing each artifact language. Each dependency
contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Target artifact kind |
| `artifactType` | string | Yes | Target logical artifact type |
| `artifactRevision` | string | Yes | Exact target revision in this release |

Every dependency target MUST occur in `artifacts` with the same kind, logical type, and revision. A publisher MUST include
every external reference that affects validation, expansion, or mapping. The publisher derives dependencies
from the artifact content and removes duplicate targets. Relationship semantics remain in the artifact content and in
dedicated manifest structures such as `schemaContextBindings`. Internal references within one payload do not create
dependency entries. A publisher MUST reject hidden network dependencies that are neither selected artifacts nor
additional resources.

#### Schema-to-context bindings

`schemaContextBindings` states which JSON-LD contexts a consumer applies to a specific schema revision. The binding uses
revisions because a binding to a logical type would change meaning when a later release selects another revision.

Each binding contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `schemaRevision` | string | Yes | Revision of a `schema` entry |
| `contextRevisions` | array | Yes | One or more revisions of `context` entries |

Every referenced revision MUST occur in `artifacts` with the required kind. A schema revision can occur in at most one
binding. A context can occur in several bindings. The context order is not semantically significant.

#### URI and IRI catalog

`uriCatalog` supports consistent online and offline resolution. It maps each resolvable URI or Internationalized Resource
Identifier (IRI) to content already selected by the manifest.

Each catalog entry contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `uri` | string | Yes | Absolute URI or IRI that can occur in an artifact |
| `artifactRevision` | string | Yes | Artifact revision that provides the document |
| `resource` | string | No | Name of the additional resource to resolve instead of the primary payload |

Each `uri` MUST occur only once. `artifactRevision` MUST identify an entry in `artifacts`. If `resource` is present, that
resource MUST occur in the selected artifact. An artifact's `canonicalUri` MUST map to that artifact's primary payload.
The registry MUST reject a catalog entry that maps an immutable URI used by a historical release to different content.
The catalog MUST contain every artifact `canonicalUri`, every JSON Schema `$id`, every JSON-LD context IRI, and every
externally expressed identifier needed to resolve the release closure.

When `resource` is absent, the URI resolves to the selected artifact's primary `payload`. When `resource` is present, the
URI resolves to that named entry in the artifact's `resources` array.

The catalog can map a stable requested IRI to an immutable context revision. The release manifest fixes that mapping for
historical use. A registry resolves the revision through the Schema Introspection API. An offline resolver uses the
selected payload or resource `bundlePath`.

#### Storage catalog

`storageCatalog` tells ingestion components which OCG stores accept records governed by a canonical schema. Source-record
schemas that only describe input data MUST NOT have a storage entry. Unmapped source data must never reach an OCG store.

Each storage entry contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Artifact kind; version 1 requires `schema` |
| `artifactType` | string | Yes | Logical schema type |
| `stores` | array | Yes | One or more registered OCG store identifiers |

The pair `(kind, artifactType)` MUST occur once in `storageCatalog`. It MUST identify a schema class that governs canonical
records. Every schema that governs canonical records MUST have one storage entry. A source-record schema MUST NOT have a
storage entry. The catalog intentionally uses logical identity instead of an artifact revision. Store placement is a
release-level association. A publisher or importer MUST reject a manifest that assigns a target store to a source-record
schema. Such an assignment identifies a malformed release manifest. Changing only a valid store association still
creates a new schema release.

Manifest format revision `1.0.0` supports these store identifiers:

| Store | Identifier |
| --- | --- |
| Data Center Inventory | `data-center-inventory` |
| Telemetry Store | `telemetry-store` |
| Insights Repository | `insights-repository` |
| Recorded Actions Repository | `recorded-actions-repository` |

A revision `1.0.0` manifest MUST use only these identifiers. The manifest MUST NOT contain physical database names,
connection data, or deployment-specific credentials.

#### Deterministic change summary

`changes` is optional. A release author does not need to provide a change summary. When present, its machine-readable
fields record the exact difference from `previousRelease`. The publication pipeline computes those fields. The optional
`summary` field is human-authored and can originate from the release description in the Schema Authoring environment.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `summary` | string | No | Human-readable release summary |
| `compatibility` | string | Yes | Overall effect: `initial`, `breaking`, `additive`, or `operational` |
| `artifacts` | array | Yes | Added, updated, and removed logical artifacts |
| `schemaContextBindings` | array | Yes | Added and removed schema-to-context pairs |
| `uriCatalog` | array | Yes | Added, changed, and removed URI mappings |
| `storageCatalog` | array | Yes | Added and removed logical-artifact-to-store pairs |

An artifact change contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `operation` | string | Yes | `add`, `update`, or `remove` |
| `kind` | string | Yes | Logical artifact kind |
| `artifactType` | string | Yes | Logical artifact type |
| `beforeRevision` | string | For `update` and `remove` | Revision selected by the predecessor |
| `afterRevision` | string | For `add` and `update` | Revision selected by this release |
| `impact` | string | Yes | `breaking`, `additive`, or `operational` |

A URI catalog change contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `operation` | string | Yes | `add`, `update`, or `remove` |
| `uri` | string | Yes | Catalog key that changed |
| `beforeRevision` | string | For `update` and `remove` | Revision selected by the predecessor |
| `beforeResource` | string | No | Predecessor resource name; absence selects its payload |
| `afterRevision` | string | For `add` and `update` | Revision selected by this release |
| `afterResource` | string | No | Current resource name; absence selects its payload |
| `impact` | string | Yes | `breaking`, `additive`, or `operational` |

A binding change contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `operation` | string | Yes | `add` or `remove` |
| `schemaRevision` | string | Yes | Bound schema revision |
| `contextRevision` | string | Yes | Bound context revision |
| `impact` | string | Yes | `breaking`, `additive`, or `operational` |

The change summary expands a binding with several contexts into one change per schema-context pair.

A storage catalog change contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `operation` | string | Yes | `add` or `remove` |
| `kind` | string | Yes | Logical artifact kind |
| `artifactType` | string | Yes | Logical artifact type |
| `store` | string | Yes | Registered OCG store identifier |
| `impact` | string | Yes | `breaking`, `additive`, or `operational` |

The change summary expands an entry with several stores into one change per logical-artifact-to-store pair.

The compatibility value is the highest impact of any change. The impact order from highest to lowest is `breaking`,
`additive`, and `operational`.

| Compatibility | Schema version increment | Meaning |
| --- | --- | --- |
| `initial` | Not applicable | The release has no predecessor |
| `breaking` | `MAJOR` | At least one consumer can reject or misinterpret data valid under the predecessor |
| `additive` | `MINOR` | The release only adds backward-compatible model capabilities |
| `operational` | `PATCH` | Bytes or associations change without changing accepted canonical data shapes |

The classifier MUST treat an unknown or unprovable effect as `breaking`. Removing an artifact is breaking. Adding a new
logical artifact is additive unless its bindings or storage placement change existing interpretation. Removing or
renaming a type or property is breaking. Adding an optional type or property is additive. A documentation correction or
equivalent mapping metadata change is operational. Removing a binding, URI mapping, or storage mapping is breaking.
Adding one is additive unless it changes the interpretation or destination of existing data, in which case it is
breaking. Updating a URI mapping is breaking. A source mapping behavior change is breaking unless validation in the
Schema Authoring environment establishes that all previously valid inputs have equivalent canonical outputs.

Source mapping behavior includes condition selection and the combined output of all applicable mappings and transformations.
Adding a mapping for an existing source schema is not automatically additive. Changing a condition can change which
canonical records ingestion produces, even when every target schema remains unchanged.

When the first release contains `changes`, all change arrays MUST be empty and compatibility MUST be `initial`. For later
releases that contain `changes`, the change arrays MUST exactly equal a comparison with the manifest identified by
`previousRelease`. The textual `summary` is descriptive and is not part of this comparison. A release with no effective
difference MUST NOT be published.

#### Ordering and canonical bytes

The publisher serializes the manifest with the JSON Canonicalization Scheme (JCS) from
[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785). Any SHA-256 digest of the manifest MUST cover the resulting UTF-8
bytes. The canonical document has no byte order mark and no trailing newline. Parsers MUST reject duplicate object member
names before canonicalization.

JCS defines object member ordering, but it preserves array order. The publisher MUST sort arrays as follows:

- `artifacts`: by `kind`, then `artifactType`.
- `resources`: by `role`, then `name`.
- `dependencies`: by `kind`, then `artifactType`, then `artifactRevision`.
- `schemaContextBindings`: by `schemaRevision`; each `contextRevisions` array by revision.
- `uriCatalog`: by `uri`.
- `storageCatalog`: by `kind`, then `artifactType`; each `stores` array by store identifier.
- `changes.artifacts`: by `kind`, `artifactType`, then `operation`.
- `changes.schemaContextBindings`: by `schemaRevision`, `contextRevision`, then `operation`.
- `changes.uriCatalog`: by `uri`, then `operation`.
- `changes.storageCatalog`: by `kind`, `artifactType`, `store`, then `operation`.

All sorted string comparisons use lexicographic order over UTF-16 code units. JCS requires this comparison for object
member names. Using the same comparison for the manifest's sorted arrays prevents implementations from producing
different array orders. Producers MUST preserve string values and MUST NOT apply Unicode normalization during
canonicalization. After ordering, the publisher serializes the complete manifest as UTF-8 bytes.

UTF-8 is the only byte encoding for the manifest. The UTF-16 rule above defines string comparison only. It does not define
the manifest's byte encoding.

#### Digest and identifier syntax

Manifest metadata uses these common lexical forms:

| Value | Required form |
| --- | --- |
| SHA-256 digest | `sha-256:` followed by 64 lowercase hexadecimal characters |
| Schema version | `0.0.0` or a higher `MAJOR.MINOR.PATCH` value without leading zeroes |
| Artifact revision | One or more RFC 3986 unreserved characters; values `.` and `..` are forbidden |
| Logical artifact type | `[a-z0-9]+(?:-[a-z0-9]+)*` |
| Store and role | `[a-z0-9]+(?:-[a-z0-9]+)*` |
| Git commit | Full lowercase hexadecimal object ID; 40 or 64 characters |

The `sha-256:` spelling applies to Schema Registry JSON metadata. It intentionally differs from the `sha256:` spelling
used by Open Container Initiative descriptors.

#### Cross-field validation

The publisher and importer enforce these invariants after structural validation:

- The pair `(kind, artifactType)` occurs once in `artifacts`.
- Each `artifactRevision` identifies one artifact entry and one set of revision-bound metadata across all releases.
- Every artifact, resource, dependency, binding, and catalog entry is unique within its containing array.
- Every digest matches the bytes and every size matches the byte length before publication succeeds.
- Every dependency, binding, and catalog reference resolves inside the same manifest.
- Every source mapping targets this release or an ancestor and passes current-release validation.
- Every source mapping resolves its source and all transformation targets to the required schema classifications.
- Every condition and transformation program reference matches a resource name, role, and digest in its mapping revision.
- Every parsed artifact reference agrees with its declared dependency, resource, binding, and catalog metadata.
- Every external runtime reference is represented by an artifact, resource, dependency, or URI catalog entry.
- When `changes` is present, its machine-readable fields match the predecessor and the schema version increment matches
  its compatibility value.
- The JCS serialization produced after parsing is byte-for-byte equal to the submitted manifest.

An importer MUST validate the manifest and all referenced bytes before it stores the release. It MUST store the release,
its immutable bytes, and its query indexes in one transaction. A separate activation transaction changes the
current-release pointer. The importer MUST NOT partially import a release or expose an inactive release through the
Schema Introspection API.

#### Complete example

The following example illustrates the complete structure. The abbreviated artifact set is suitable for explaining the
format, but it does not define the OCG's required logical schemas. The example uses indentation for readability. A
publisher serializes the same data with JCS before it computes the manifest digest.

```json
{
  "$schema": "https://schemas.example.org/ocg/schema-release-manifest-1.0.json",
  "formatVersion": "1.0.0",
  "schemaVersion": "2.4.0",
  "previousRelease": {
    "schemaVersion": "2.3.1",
    "manifestDigest": "sha-256:8a3105c7d52e131a8666bc285b3d5bd59b8c30d22a20e88e2f24dad9f48ee4a4"
  },
  "source": {
    "repository": "https://git.example.org/ocg/schema-model",
    "commit": "9c4051d7a4be1234567890abcdef1234567890ab"
  },
  "artifacts": [
    {
      "kind": "context",
      "artifactType": "device",
      "artifactRevision": "01K4Q7YD0SZWT51M15FJ9C4QSD",
      "createdUnderSchemaVersion": "2.3.1",
      "canonicalUri": "https://schemas.example.org/resources/context/device/01K4Q7YD0SZWT51M15FJ9C4QSD",
      "payload": {
        "mediaType": "application/ld+json;profile=\"http://www.w3.org/ns/json-ld#context\"",
        "digest": "sha-256:04b3d13e85d30f5847d889e23d222f304ca70090113f344fcc34026b552a81ed",
        "size": 1204,
        "bundlePath": "artifacts/context/device/01K4Q7YD0SZWT51M15FJ9C4QSD.jsonld"
      },
      "resources": [],
      "dependencies": []
    },
    {
      "kind": "schema",
      "artifactType": "device",
      "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
      "createdUnderSchemaVersion": "2.1.0",
      "schemaClass": "entity-type",
      "canonicalUri": "https://schemas.example.org/resources/schema/device/01K4Q7S2YV4B5MC7B8G8DCDR5F",
      "payload": {
        "mediaType": "application/schema+json",
        "digest": "sha-256:c14ae46a31d223a7030ed3f04e8944d634eefcab35f84b357f11d9a02d884a01",
        "size": 4812,
        "bundlePath": "artifacts/schema/device/01K4Q7S2YV4B5MC7B8G8DCDR5F.json"
      },
      "resources": [],
      "dependencies": []
    },
    {
      "kind": "schema",
      "artifactType": "example-cmdb-device",
      "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
      "createdUnderSchemaVersion": "2.4.0",
      "schemaClass": "source-record",
      "canonicalUri": "https://schemas.example.org/resources/schema/example-cmdb-device/01K4Q80PQ2J6R0A7KX9N3FHTVE",
      "payload": {
        "mediaType": "application/schema+json",
        "digest": "sha-256:93be731e9d96b015174477dba55fc5cf3a00cf3627ee75cd6cb73a252127d5d6",
        "size": 2711,
        "bundlePath": "artifacts/schema/example-cmdb-device/01K4Q80PQ2J6R0A7KX9N3FHTVE.json"
      },
      "resources": [],
      "dependencies": []
    },
    {
      "kind": "schema",
      "artifactType": "located-in",
      "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6",
      "createdUnderSchemaVersion": "2.1.0",
      "schemaClass": "edge-type",
      "canonicalUri": "https://schemas.example.org/resources/schema/located-in/01K4Q7T3ZW5C6ND8C9H9EDE4G6",
      "payload": {
        "mediaType": "application/schema+json",
        "digest": "sha-256:d14ae46a31d223a7030ed3f04e8944d634eefcab35f84b357f11d9a02d884a01",
        "size": 1824,
        "bundlePath": "artifacts/schema/located-in/01K4Q7T3ZW5C6ND8C9H9EDE4G6.json"
      },
      "resources": [],
      "dependencies": []
    },
    {
      "kind": "source-mapping",
      "artifactType": "example-cmdb-device",
      "artifactRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
      "createdUnderSchemaVersion": "2.4.0",
      "targetSchemaVersion": "2.4.0",
      "canonicalUri": "https://schemas.example.org/resources/source-mapping/example-cmdb-device/01K4Q82H6BP2R6M7MA9W5K3Q1T",
      "payload": {
        "mediaType": "application/vnd.apeiro.ocg.source-mapping.v1+json",
        "digest": "sha-256:84de32c10d04a3cf9d0fe1b1f4d6e7a95360a64dd64c11da078307b8dd7785c3",
        "size": 2370,
        "bundlePath": "artifacts/source-mapping/example-cmdb-device/descriptor.json"
      },
      "resources": [
        {
          "name": "example-cmdb-device-condition.ocgdsl",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+ocgdsl",
          "digest": "sha-256:2ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 348,
          "bundlePath": "artifacts/source-mapping/example-cmdb-device/example-cmdb-device-condition.ocgdsl"
        },
        {
          "name": "example-cmdb-device-location.json",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
          "digest": "sha-256:3ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 1024,
          "bundlePath": "artifacts/source-mapping/example-cmdb-device/example-cmdb-device-location.json"
        },
        {
          "name": "example-cmdb-device-transformation.json",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
          "digest": "sha-256:1ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 1584,
          "bundlePath": "artifacts/source-mapping/example-cmdb-device/example-cmdb-device-transformation.json"
        }
      ],
      "dependencies": [
        {
          "kind": "schema",
          "artifactType": "device",
          "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F"
        },
        {
          "kind": "schema",
          "artifactType": "example-cmdb-device",
          "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE"
        },
        {
          "kind": "schema",
          "artifactType": "located-in",
          "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6"
        }
      ]
    }
  ],
  "schemaContextBindings": [
    {
      "schemaRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
      "contextRevisions": [
        "01K4Q7YD0SZWT51M15FJ9C4QSD"
      ]
    }
  ],
  "uriCatalog": [
    {
      "uri": "https://schema.example.org/ocg/contexts/device",
      "artifactRevision": "01K4Q7YD0SZWT51M15FJ9C4QSD"
    },
    {
      "uri": "https://schemas.example.org/resources/context/device/01K4Q7YD0SZWT51M15FJ9C4QSD",
      "artifactRevision": "01K4Q7YD0SZWT51M15FJ9C4QSD"
    },
    {
      "uri": "https://schemas.example.org/resources/schema/device/01K4Q7S2YV4B5MC7B8G8DCDR5F",
      "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F"
    },
    {
      "uri": "https://schemas.example.org/resources/schema/example-cmdb-device/01K4Q80PQ2J6R0A7KX9N3FHTVE",
      "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE"
    },
    {
      "uri": "https://schemas.example.org/resources/schema/located-in/01K4Q7T3ZW5C6ND8C9H9EDE4G6",
      "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6"
    },
    {
      "uri": "https://schemas.example.org/resources/source-mapping/example-cmdb-device/01K4Q82H6BP2R6M7MA9W5K3Q1T",
      "artifactRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T"
    }
  ],
  "storageCatalog": [
    {
      "kind": "schema",
      "artifactType": "device",
      "stores": [
        "data-center-inventory"
      ]
    },
    {
      "kind": "schema",
      "artifactType": "located-in",
      "stores": [
        "data-center-inventory"
      ]
    }
  ],
  "changes": {
    "summary": "Add the example CMDB device source schema and mapping.",
    "compatibility": "additive",
    "artifacts": [
      {
        "operation": "add",
        "kind": "schema",
        "artifactType": "example-cmdb-device",
        "afterRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
        "impact": "additive"
      },
      {
        "operation": "add",
        "kind": "source-mapping",
        "artifactType": "example-cmdb-device",
        "afterRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "impact": "additive"
      }
    ],
    "schemaContextBindings": [],
    "uriCatalog": [
      {
        "operation": "add",
        "uri": "https://schemas.example.org/resources/schema/example-cmdb-device/01K4Q80PQ2J6R0A7KX9N3FHTVE",
        "afterRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
        "impact": "additive"
      },
      {
        "operation": "add",
        "uri": "https://schemas.example.org/resources/source-mapping/example-cmdb-device/01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "afterRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "impact": "additive"
      }
    ],
    "storageCatalog": []
  }
}
```

## Schema Introspection API

The Schema Registry exposes a read-only introspection interface.

### Requirements

- Every response of the introspection interface MUST carry the current schema version so consumers can detect schema
  advancement between queries.
- The introspection interface MUST support conditional queries that allow consumers to retrieve schema data only when
  the version has changed since their last request.

### API specification

#### Scope and design principles

Version `v1` is a read-only HTTP API. It exposes activated releases, artifact metadata, artifact bytes, and relationships
from validated release manifests. It does not expose the publication or import workflow.

The API follows these principles:

- A request that depends on release state MUST identify one explicit `schemaVersion`.
- All relationships in one response MUST resolve within the selected release.
- Metadata resources MUST link to immutable artifact bytes instead of embedding them by default.
- Artifact payload endpoints MUST return the exact stored bytes after removal of any HTTP content coding.
- The API MUST derive artifact relationships from the validated release manifest and parsed artifact descriptors.
- The API MUST NOT read from the authoring Git repository while serving a request.
- The API MUST NOT expose a mutable, unversioned alias for release-scoped resources.

The explicit schema version prevents a consumer from combining resources from different releases. A consumer first reads
`GET /v1/registry`, then uses the returned version in all release-scoped requests. If the current version advances during
that workflow, the versioned resources remain unchanged.

All paths below are relative to the Schema Registry origin. The API major version is part of the path. A breaking HTTP
contract change requires a new major path such as `/v2`.

#### HTTP conventions

The API MUST support `GET` and `HEAD` for every resource in this specification. A `HEAD` response MUST return the same
status and headers as `GET`, without a response body.

API-generated JSON uses the media type `application/json`. Exact manifests, artifact payloads, and additional resources
retain their declared media types. The server MUST return `406 Not Acceptable` when it cannot provide a representation
allowed by `Accept`.

All API-generated JSON MUST satisfy the I-JSON constraints in RFC 7493. JSON member names use `camelCase`. Clients MUST
ignore unknown JSON members. Clients MUST treat an unknown enum value as unsupported instead of assigning it a known
meaning.

Every response generated after the first release becomes active MUST include `Schema-Version` and `Cache-Control`.
Successful responses that select a representation MUST also include `ETag`.

| Header | Meaning |
| --- | --- |
| `Schema-Version` | Schema version that is current when the server evaluates the request |
| `Cache-Control` | Revalidation policy; version `v1` requires `no-cache` |
| `ETag` | Opaque validator for the selected representation |

`Schema-Version` contains an unquoted semantic version, for example `Schema-Version: 2.4.0`. It identifies the current
release, not necessarily the release selected by the request. A release-scoped JSON body also contains its selected
`schemaVersion`. Every release-scoped response also carries `Selected-Schema-Version`, including metadata, bytes,
redirects, and `304 Not Modified` responses. A release-scoped error carries this header only after the requested release
has resolved successfully.

The `Schema-Version` header can change while an immutable response body remains unchanged. Therefore, all responses use
`Cache-Control: no-cache`. A client can store a response, but it must revalidate the response before reuse. A successful
`304 Not Modified` response carries the current `Schema-Version` header.

The registry MUST evaluate the current-release pointer once per request. It MUST use the same value for the
`Schema-Version` header and any current-release data in the response body.

If no release is active, the API MUST return `503 Service Unavailable`. This is the only response for which
`Schema-Version` is absent because no current schema version exists.

##### Gzip content encoding

Every resource method MUST support the `identity` and `gzip` content codings. This requirement applies to successful and
error representations returned for `GET` and `HEAD`. Responses without a body, including `304 Not Modified` and
redirects, do not apply a content coding.

The server MUST select a content coding according to `Accept-Encoding` and its quality values. When `gzip` is acceptable,
the server MUST be able to select it. A gzip response MUST include `Content-Encoding: gzip`. The response media type does
not change. If neither `identity` nor `gzip` is acceptable, the server MUST return `406 Not Acceptable`.

Responses subject to content-coding negotiation MUST include `Vary: Accept-Encoding`. This includes an identity response
when the corresponding resource also supports gzip. A `304 Not Modified` response MUST include the same `Vary` value as
the selected representation.

The gzip stream MUST conform to RFC 1952. Decompressing it MUST produce the bytes of the identity representation. For an
exact manifest, artifact payload, or additional resource, the decompressed bytes MUST equal the stored bytes. The
`Manifest-Digest`, `Artifact-Digest`, descriptor `digest`, and descriptor `size` values describe those uncompressed stored
bytes. If the server sends `Content-Length` with gzip, its value MUST describe the compressed response body.

A `HEAD` request uses the same content-coding negotiation as `GET`. When gzip is selected, its response MUST include
`Content-Encoding: gzip` and the entity tag for the gzip representation. It contains no gzip stream because a `HEAD`
response has no body.

#### Resource overview

Version `v1` defines these resource methods:

| Method and path | Purpose |
| --- | --- |
| `GET /v1/registry` | Discover the current release and top-level resources |
| `GET /v1/releases` | List activated releases |
| `GET /v1/releases/{schemaVersion}` | Get one release descriptor and change summary |
| `GET /v1/releases/{schemaVersion}/manifest` | Get the exact release manifest bytes |
| `GET /v1/releases/{schemaVersion}/artifacts` | List or filter artifacts selected by a release |
| `GET /v1/releases/{schemaVersion}/artifacts/{kind}/{artifactType}` | Get one release-selected artifact descriptor |
| `GET /v1/releases/{schemaVersion}/schemas` | List schemas selected by a release |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}` | Get schema metadata and relationship links |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/content` | Get the exact JSON Schema bytes |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/contexts` | List contexts bound to a selected schema revision |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/package` | Get a schema and its contexts in one JSON document |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/source-mappings` | List source mappings for a source schema |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/source-mappings/packages` | List source mapping packages for a source schema |
| `GET /v1/releases/{schemaVersion}/schemas/{artifactType}/stores` | Get the OCG stores assigned to a logical schema |
| `GET /v1/releases/{schemaVersion}/source-mappings` | List source mappings and filter them by source or target schema |
| `GET /v1/releases/{schemaVersion}/source-mappings/{artifactType}` | Get one release-selected source mapping artifact |
| `GET /v1/releases/{schemaVersion}/source-mappings/{artifactType}/package` | Get one source mapping package |
| `GET /v1/releases/{schemaVersion}/resolve?uri={uri}` | Resolve a release-scoped URI or IRI |
| `GET /v1/artifacts/{artifactRevision}/metadata` | Get metadata for one immutable artifact revision |
| `GET /v1/artifacts/{artifactRevision}` | Get the exact primary payload of an artifact revision |
| `GET /v1/artifacts/{artifactRevision}/resources/{resourceName}` | Get an additional artifact resource |
| `GET /v1/artifact-kinds/{kind}/{artifactType}/history` | List release selections for one logical artifact |
| `GET /v1/changes?from={oldVersion}&to={newVersion}` | Compare two releases in the same history chain |
| `GET /v1/resources/{kind}/{artifactType}/{artifactRevision}` | Resolve a canonical artifact resource path |

`{schemaVersion}` MUST use the schema version syntax from this document. `{kind}` and `{artifactType}` are case-sensitive.
The initial `{kind}` values are `schema`, `context`, and `source-mapping`. An unsupported kind produces a `400` response.

For the schema-nested source-mapping resources, `{artifactType}` identifies the source schema logical artifact type. The
resolved source mappings can have different logical artifact types, which their metadata exposes.
For the direct source-mapping metadata and package resources, `{artifactType}` identifies the mapping's own logical
artifact type.

`{artifactRevision}` is opaque. Version `v1` restricts it to the URI-safe artifact revision syntax defined in this
document. Clients MUST copy it without interpreting or normalizing it. `{resourceName}` is a single percent-encoded path
segment. The decoded value MUST exactly equal a resource name in the artifact descriptor.

#### Common link and artifact representations

Links use URI references. A client MUST resolve a relative link against the URI of the response that contains it. The
server MUST NOT require clients to construct artifact or relationship paths from identifiers.

An artifact descriptor represents one immutable revision. It contains these fields:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Artifact kind |
| `artifactType` | string | Yes | Logical artifact type within the kind |
| `artifactRevision` | string | Yes | Opaque immutable revision |
| `createdUnderSchemaVersion` | string | Yes | First release that selected this revision |
| `schemaClass` | string | For `schema` | Registered schema classification |
| `targetSchemaVersion` | string | For `source-mapping` | Canonical release targeted by all transformations |
| `canonicalUri` | string | Yes | Absolute immutable URI for the primary payload |
| `payload` | object | Yes | Primary byte descriptor and retrieval link |
| `resources` | array | Yes | Additional byte descriptors and retrieval links |
| `dependencies` | array | Yes | Exact artifact dependencies |
| `extensions` | object | No | Namespaced extension data copied from the manifest entry |
| `links` | object | Yes | Navigation links for this API representation |

The API payload descriptor contains `mediaType`, `digest`, `size`, and `href`. It does not contain `bundlePath` because
that field belongs to offline release packaging. Each resource descriptor adds `name` and `role`.

Each dependency contains `kind`, `artifactType`, `artifactRevision`, and `href`. For a release-scoped representation,
`href` points to the dependency descriptor in that release. For revision metadata, `href` points to the immutable
revision metadata.

The `links` object contains `self`, `content`, and `history`. A schema descriptor can also contain `contexts`, `package`,
`sourceMappings`, and `stores`. A release-scoped source-mapping descriptor also contains `package`. Revision metadata
remains release-independent and has no package link. Clients MUST follow the returned links instead of modifying the
`self` URI.

The registry MUST return an artifact descriptor whose bound values match the release manifest. The API MUST NOT add
mutable metadata to the artifact revision representation.

#### Pagination and filtering

Collection resources use cursor pagination. They accept `limit` and `cursor` query parameters.

| Parameter | Required | Rules |
| --- | --- | --- |
| `limit` | No | Integer from `1` through `1000`; default `100` |
| `cursor` | No | Opaque value returned by the same resource and filter set |

A collection response contains:

```json
{
  "schemaVersion": "2.4.0",
  "items": [],
  "nextCursor": null,
  "links": {
    "self": "/v1/releases/2.4.0/artifacts?limit=100"
  }
}
```

`nextCursor` is `null` when no later page exists. Otherwise, the client passes it unchanged as `cursor`. A cursor MUST
bind the resource, sort order, filters, and collection snapshot. The server MUST reject a cursor used with different
parameters.

Release-scoped collections are immutable, and their cursors MUST NOT expire. Mutable collections, such as release
history, MUST keep the snapshot selected by the first page for the cursor lifetime. The server MUST return
`400 Invalid Cursor` for a malformed cursor and `410 Cursor Expired` for an expired mutable cursor.

The server MUST reject unknown query parameters with `400 Invalid Query Parameter`. This rule prevents a misspelled
filter from silently broadening a query.

#### Registry discovery

`GET /v1/registry` is the change-detection and entry resource. It returns the current schema version, the exact manifest
digest, and links to current release resources.

```http
GET /v1/registry HTTP/1.1
Host: schemas.example.org
Accept: application/json
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
Schema-Version: 2.4.0
ETag: "registry-view-fce21a"
Cache-Control: no-cache
```

```json
{
  "schemaVersion": "2.4.0",
  "manifestDigest": "sha-256:2cb43b63cb826fc45ef1b8610df9f79156d8a231f9e660b0409a46c34df15fca",
  "links": {
    "self": "/v1/registry",
    "release": "/v1/releases/2.4.0",
    "manifest": "/v1/releases/2.4.0/manifest",
    "artifacts": "/v1/releases/2.4.0/artifacts",
    "schemas": "/v1/releases/2.4.0/schemas",
    "sourceMappings": "/v1/releases/2.4.0/source-mappings",
    "releases": "/v1/releases"
  }
}
```

The `ETag` identifies the complete discovery representation. It can change when a link changes, even when
`schemaVersion` remains unchanged. The server MUST NOT derive this entity tag only from the schema version.

The client performs conditional change detection by sending the last entity tag in `If-None-Match`. An unchanged
representation produces `304 Not Modified` without a body. A changed representation produces `200 OK`, a new body, and
a new entity tag.

#### Release resources

##### List releases

`GET /v1/releases` lists activated releases from newest to oldest in activation-chain order. It supports cursor pagination
and no other filters. Each item contains:

- `schemaVersion`
- `manifestDigest`
- `previousRelease`
- `source`
- `compatibility`
- `links`

`compatibility` is absent when the manifest has no `changes` object. `previousRelease`, `source`, and `compatibility`
retain the meanings defined by the manifest format.

##### Get a release descriptor

`GET /v1/releases/{schemaVersion}` returns metadata for one release. The representation contains the same fields as a
release-list item. It also contains the complete `changes` object when the manifest provides one.

```json
{
  "schemaVersion": "2.4.0",
  "manifestDigest": "sha-256:2cb43b63cb826fc45ef1b8610df9f79156d8a231f9e660b0409a46c34df15fca",
  "previousRelease": {
    "schemaVersion": "2.3.1",
    "manifestDigest": "sha-256:8a3105c7d52e131a8666bc285b3d5bd59b8c30d22a20e88e2f24dad9f48ee4a4"
  },
  "source": {
    "repository": "https://git.example.org/ocg/schema-model",
    "commit": "9c4051d7a4be1234567890abcdef1234567890ab"
  },
  "links": {
    "self": "/v1/releases/2.4.0",
    "manifest": "/v1/releases/2.4.0/manifest",
    "artifacts": "/v1/releases/2.4.0/artifacts",
    "schemas": "/v1/releases/2.4.0/schemas",
    "sourceMappings": "/v1/releases/2.4.0/source-mappings"
  }
}
```

##### Get a release manifest

`GET /v1/releases/{schemaVersion}/manifest` returns the exact JCS-serialized manifest bytes. The response uses
`application/vnd.apeiro.ocg.schema-release-manifest.v1+json`. It includes `Selected-Schema-Version` and
`Manifest-Digest` headers.

The server MUST NOT reformat or filter the manifest. The identity representation and the decompressed gzip representation
MUST hash to the `Manifest-Digest` value. The strong `ETag` for the identity representation SHOULD equal the quoted
manifest digest.

The registry MUST retain every activated release, its manifest, and every referenced artifact byte for the lifetime of
the registry deployment. It MUST continue to serve historical release resources after a newer release becomes current.
The API MUST NOT resolve an imported but inactive release by schema version. This retention and visibility rule makes
version-pinned ingestion and release history reproducible.

Version `v1` does not define a release bundle endpoint. The manifest specifies paths inside an offline bundle, but it does
not specify deterministic archive bytes. A later contract can add a bundle after defining its archive format and digest.

#### Artifact discovery

##### List release artifacts

`GET /v1/releases/{schemaVersion}/artifacts` returns artifact descriptors selected by the release. It sorts results by
`kind`, then `artifactType`. It supports these exact-match filters:

| Parameter | Meaning |
| --- | --- |
| `kind` | Include only one registered artifact kind |
| `artifactType` | Include only one logical artifact type |
| `schemaClass` | Include only schemas with this classification |
| `dependsOnRevision` | Include artifacts with a dependency on this exact revision |

Filters combine with logical `AND`. `schemaClass` without `kind=schema` produces `400 Invalid Filter Combination`. An
unknown but well-formed artifact type returns an empty collection. An unknown schema classification produces
`400 Unsupported Schema Class`.

##### Get a release-selected artifact

`GET /v1/releases/{schemaVersion}/artifacts/{kind}/{artifactType}` resolves a logical identity through the selected
release manifest. It returns the corresponding artifact descriptor. It returns `404 Artifact Not In Release` if the
release does not select that logical artifact.

This resource is the generic metadata lookup for all artifact kinds. Kind-specific resources add relationship data but
resolve to the same immutable revision.

##### Get revision metadata and bytes

`GET /v1/artifacts/{artifactRevision}/metadata` returns the common descriptor for one immutable revision. Its links do
not depend on a selected release. Release-level associations, including contexts and stores, are absent.

`GET /v1/artifacts/{artifactRevision}` returns the exact primary payload bytes. The response MUST use the payload's
declared media type. It MUST include `Artifact-Revision` and `Artifact-Digest` headers.

`GET /v1/artifacts/{artifactRevision}/resources/{resourceName}` returns one exact additional resource. It MUST include
`Artifact-Revision`, `Resource-Name`, and `Artifact-Digest` headers. `Artifact-Digest` contains the resource digest for
this response.

The server MUST NOT transcode or reformat exact artifact bytes. The identity representation and the decompressed gzip
representation MUST match the size and digest in the artifact descriptor. A strong `ETag` for the identity representation
SHOULD equal the quoted content digest.

##### Resolve a canonical resource path

`GET /v1/resources/{kind}/{artifactType}/{artifactRevision}` returns the same primary payload as
`GET /v1/artifacts/{artifactRevision}`. It additionally verifies that the kind and logical type match the revision. A
coordinate mismatch returns `404 Artifact Not Found`.

The registry SHOULD create artifact `canonicalUri` values that resolve to this resource shape on an origin controlled by
the registry deployment. The registry MUST continue to serve every published canonical URI under its control. It MUST
never redirect a canonical URI to mutable content. A publisher MUST prove that another authority serves and preserves a
canonical URI before the registry accepts an external origin.

#### Schema resources

##### List schemas

`GET /v1/releases/{schemaVersion}/schemas` is the schema-specific view of the artifact collection. It sorts results by
`artifactType` and supports these filters:

| Parameter | Meaning |
| --- | --- |
| `schemaClass` | Include only one registered schema classification |
| `dependsOnRevision` | Include schemas with a dependency on this exact revision |

Each item is an artifact descriptor with schema relationship links. The initial schema classifications are
`entity-type`, `identifier-type`, `edge-type`, and `source-record`.

##### Get schema metadata

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}` resolves the logical schema through one release. It returns the
schema descriptor and links to its content, associated contexts, source mappings, package, stores, and history.

The `sourceMappings` link points to
`/v1/releases/{schemaVersion}/source-mappings?sourceArtifactType={artifactType}`. It discovers all mappings that accept
records conforming to the selected schema revision. It does not assume that a source mapping has the same logical artifact
type as its source schema.

##### Get JSON Schema content

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/content` returns the exact JSON Schema bytes. It is the direct
lookup Data Ingestion uses for the pair `(logical artifact type, schema version)`.

The response uses `application/schema+json`. It includes `Selected-Schema-Version`, `Artifact-Revision`, and
`Artifact-Digest`. The response bytes and digest equal the selected artifact payload.

For example:

```http
GET /v1/releases/2.4.0/schemas/example-cmdb-device/content HTTP/1.1
Host: schemas.example.org
Accept: application/schema+json
Accept-Encoding: gzip
```

```http
HTTP/1.1 200 OK
Content-Type: application/schema+json
Content-Encoding: gzip
Vary: Accept-Encoding
Schema-Version: 2.4.0
Selected-Schema-Version: 2.4.0
Artifact-Revision: 01K4Q80PQ2J6R0A7KX9N3FHTVE
Artifact-Digest: sha-256:93be731e9d96b015174477dba55fc5cf3a00cf3627ee75cd6cb73a252127d5d6
ETag: "sha-256:93be731e9d96b015174477dba55fc5cf3a00cf3627ee75cd6cb73a252127d5d6-gzip"
Cache-Control: no-cache
```

##### List associated contexts

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/contexts` returns the contexts bound to the exact schema
revision selected by the release. It is a non-paginated relationship resource because one manifest binding contains the
complete context set.

The response contains `schemaVersion`, a `schema` reference, `items`, and `links`. Each item is a complete common artifact
descriptor for a context. Items use ascending `artifactRevision` order because context order has no semantic meaning. A
schema without a binding returns `200 OK` with an empty `items` array.

The resource returns `404 Schema Not In Release` only when the release does not select the requested schema. It MUST NOT
use `404` for a selected schema with no context binding.

##### Get a schema package

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/package` returns one schema and all contexts bound to that exact
schema revision. It is a convenience resource for clients that need both validation and semantic expansion data.

```json
{
  "schemaVersion": "2.4.0",
  "schema": {
    "metadata": {
      "kind": "schema",
      "artifactType": "device",
      "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
      "schemaClass": "entity-type"
    },
    "document": {}
  },
  "contexts": [
    {
      "metadata": {
        "kind": "context",
        "artifactType": "device",
        "artifactRevision": "01K4Q7YD0SZWT51M15FJ9C4QSD"
      },
      "document": {
        "@context": {}
      }
    }
  ],
  "links": {
    "self": "/v1/releases/2.4.0/schemas/device/package",
    "schema": "/v1/releases/2.4.0/schemas/device/content"
  }
}
```

`metadata` in this representation is the complete common artifact descriptor. The abbreviated example omits common
fields only for readability. `document` contains the parsed JSON value. It can be an object or boolean for a JSON Schema.

Contexts appear in ascending `artifactRevision` order because binding order has no semantic meaning. A missing binding
produces an empty `contexts` array. The server MUST reject the package request with `406 Not Acceptable` if the client
does not accept an API-generated JSON representation.

#### Source mapping discovery

`GET /v1/releases/{schemaVersion}/source-mappings` returns source mappings selected by a release. It sorts mappings by
`artifactType` and supports these exact-match filters:

| Parameter | Meaning |
| --- | --- |
| `sourceArtifactType` | Mapping source schema logical artifact type |
| `targetArtifactType` | Logical artifact type targeted by at least one transformation |
| `engineLanguage` | `engine.language` in the mapping descriptor |
| `executionProfile` | `engine.executionProfile` in the mapping descriptor |

Filters combine with logical `AND`. The registry resolves `sourceSchema` and every transformation's `targetSchema` during
import. Queries compare resolved schema dependencies, not only descriptor text. A target filter matches a mapping once,
even when several transformations target that schema. Matching returns the complete mapping, not a filtered transformation
list.

`sourceArtifactType` can match zero, one, or several mappings for the selected source schema revision. Data Ingestion
uses this collection to discover all candidates and follows every page before evaluating mapping conditions. The registry
does not filter candidates based on source-record values. Each item's metadata and package links select that mapping
explicitly.

When `sourceArtifactType` or `targetArtifactType` is present, the named schema MUST exist in the selected release. An
unknown schema produces `404 Schema Not In Release`. An existing schema with no matching mappings produces `200 OK` with
an empty `items` array.

Each item uses the shared source mapping artifact model. The direct metadata endpoint returns the same model as one
object. Schema-nested metadata collections and the `mapping` field in packages MUST also use this model.
The model contains the complete common source-mapping artifact descriptor and these additional fields:

| Field | JSON type | Description |
| --- | --- | --- |
| `sourceSchema` | object | Logical type, exact revision, and links for the resolved source schema |
| `engine` | object | Language, language version, and execution profile |
| `programs` | object | Optional condition and non-empty transformation program list with resolved targets |

`programs.condition`, when present, contains `name`, `role`, `mediaType`, `digest`, `size`, and `href`. Each
`programs.transformations` entry contains those same resource descriptor fields, the output `cardinality`, and a resolved
`targetSchema`. The array preserves the descriptor's transformation order.

Each program's `name` MUST equal its descriptor entry's `programRef`. Its `digest` MUST equal that entry's `programDigest`.
All six resource descriptor fields MUST match the corresponding item in the common descriptor's `resources` array.
`href` retrieves the program from the mapping's immutable revision. Condition and transformation resources retain role
`program`. The API uses `name` and `digest`, not the stored descriptor's `programRef` and `programDigest` field names.

`sourceSchema` and each transformation's `targetSchema` use the same fields: `artifactType`, `artifactRevision`, and
`content`. The `content` link is scoped to the requested release.

For example, a client can filter the source-mapping catalog by one incoming source-record type with:

```http
GET /v1/releases/2.4.0/source-mappings?sourceArtifactType=example-cmdb-device HTTP/1.1
Host: schemas.example.org
Accept: application/json
```

```json
{
  "schemaVersion": "2.4.0",
  "items": [
    {
      "kind": "source-mapping",
      "artifactType": "example-cmdb-device",
      "artifactRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
      "targetSchemaVersion": "2.4.0",
      "sourceSchema": {
        "artifactType": "example-cmdb-device",
        "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
        "content": "/v1/releases/2.4.0/schemas/example-cmdb-device/content"
      },
      "engine": {
        "language": "ocg-dsl",
        "languageVersion": "1.0.0",
        "executionProfile": "ocg-custom-transformations"
      },
      "programs": {
        "condition": {
          "name": "example-cmdb-device-condition.ocgdsl",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+ocgdsl",
          "digest": "sha-256:2ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 348,
          "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-condition.ocgdsl"
        },
        "transformations": [
          {
            "name": "example-cmdb-device-transformation.json",
            "role": "program",
            "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
            "digest": "sha-256:1ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
            "size": 1584,
            "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-transformation.json",
            "targetSchema": {
              "artifactType": "device",
              "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
              "content": "/v1/releases/2.4.0/schemas/device/content"
            },
            "cardinality": "one"
          },
          {
            "name": "example-cmdb-device-location.json",
            "role": "program",
            "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
            "digest": "sha-256:3ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
            "size": 1024,
            "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-location.json",
            "targetSchema": {
              "artifactType": "located-in",
              "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6",
              "content": "/v1/releases/2.4.0/schemas/located-in/content"
            },
            "cardinality": "one"
          }
        ]
      },
      "links": {
        "self": "/v1/releases/2.4.0/source-mappings/example-cmdb-device",
        "content": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "package": "/v1/releases/2.4.0/source-mappings/example-cmdb-device/package",
        "history": "/v1/artifact-kinds/source-mapping/example-cmdb-device/history"
      }
    }
  ],
  "nextCursor": null,
  "links": {
    "self": "/v1/releases/2.4.0/source-mappings?sourceArtifactType=example-cmdb-device"
  }
}
```

The abbreviated item omits common descriptor fields only for readability. Actual responses MUST include the complete
common artifact descriptor.

##### Get source mapping metadata

`GET /v1/releases/{schemaVersion}/source-mappings/{artifactType}` returns the source mapping selected by the release.
The path identifies the mapping's own logical artifact type, not its source schema's logical artifact type.

The response uses `application/json` and the shared source mapping artifact model defined above. Its body MUST match
the corresponding `items` entry from either source-mapping metadata collection for the same release.
The endpoint returns the artifact object directly, without an `items` array or pagination fields.
Its `schemaVersion` identifies the selected release. Its `self` and `package` links point to the direct endpoints.

Every release-scoped source-mapping artifact MUST use these links, including artifacts in collections and packages:

- `self`: `/v1/releases/{schemaVersion}/source-mappings/{artifactType}`.
- `package`: `/v1/releases/{schemaVersion}/source-mappings/{artifactType}/package`.

Both links use the mapping's own logical artifact type and the selected release. They return single objects, not filtered
collections. This rule also applies to source mappings returned by generic release artifact endpoints.

The endpoint returns `404 Artifact Not In Release` with code `artifact-not-in-release` when the release does not select
the requested source mapping. It does not evaluate mapping conditions.

##### List source mapping metadata

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/source-mappings` lists the source mappings whose resolved
source schema has the logical artifact type `{artifactType}`. Each item uses the enriched representation defined for
source mapping discovery. Each item MUST use the same source mapping artifact model as the direct metadata endpoint.
It contains the mapping's identity, source schema, engine, cardinality, optional condition, and all transformations
with their resolved targets.

The endpoint MUST accept the optional exact-match filter `sourceMappingArtifactType`. The filter compares the mapping's
own `artifactType`, not its source schema's logical artifact type. A mapping selected by the release but associated
with another source schema does not match the filter. An unknown, well-formed mapping type also produces an empty
collection. A malformed filter produces `400 Invalid Query Parameter`.

The schema-nested metadata and package collection endpoints share these collection and filtering rules:

- Both endpoints MUST use the common collection envelope: `schemaVersion`, `items`, `nextCursor`, and `links`.
- Both endpoints MUST support the common `limit` and `cursor` pagination parameters.
- Both endpoints MUST restrict results to mappings for the source schema revision selected by the requested release.
- Both endpoints MUST accept the optional exact-match filter `sourceMappingArtifactType`, using logical artifact type syntax.
- Without the filter, both endpoints MUST list all candidate mappings, subject to pagination.
- With a matching filter, both endpoints MUST return an `items` array containing exactly one mapping or package.
- With no matching mappings, both endpoints MUST return `200 OK` with empty `items` and `nextCursor: null`.
- With an unknown source schema, both endpoints MUST return `404 Schema Not In Release`.

The response shape MUST NOT change when zero, one, or several mappings match. The registry does not evaluate mapping
conditions while filtering results.

Metadata items use ascending `artifactType` order. Package items use the same order through `mapping.artifactType`.
Cursors bind the source schema, filter, and selected release under the common pagination rules. Clients follow every
page to retrieve all candidate mappings or packages.

The descriptor and all programs remain separate immutable byte resources. Clients retrieve the descriptor through
`payload.href`. They retrieve each condition or transformation through its `href` or the matching `resources` entry.

For example, this request filters mappings for the `example-cmdb-device` source schema by one mapping's logical artifact
type. A matching filter still returns a collection:

```http
GET /v1/releases/2.4.0/schemas/example-cmdb-device/source-mappings?sourceMappingArtifactType=example-cmdb-device HTTP/1.1
Host: schemas.example.org
Accept: application/json
```

```json
{
  "schemaVersion": "2.4.0",
  "items": [
    {
      "schemaVersion": "2.4.0",
      "kind": "source-mapping",
      "artifactType": "example-cmdb-device",
      "artifactRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
      "links": {
        "self": "/v1/releases/2.4.0/source-mappings/example-cmdb-device",
        "package": "/v1/releases/2.4.0/source-mappings/example-cmdb-device/package"
      }
    }
  ],
  "nextCursor": null,
  "links": {
    "self": "/v1/releases/2.4.0/schemas/example-cmdb-device/source-mappings?sourceMappingArtifactType=example-cmdb-device"
  }
}
```

The example abbreviates the item. Actual items MUST include the complete enriched source-mapping representation from
source mapping discovery.

##### Get a source mapping package

`GET /v1/releases/{schemaVersion}/source-mappings/{artifactType}/package` returns the package for one release-selected
source mapping. The path identifies the mapping's own logical artifact type.

The response uses `application/json` and the shared source mapping package model defined below. Its body MUST match
the corresponding `items` entry from the schema-nested package collection for the same release.
The endpoint returns the package object directly, without a collection envelope or pagination fields.
Its `schemaVersion` identifies the selected release. Its `ETag` identifies the complete package representation.

The endpoint returns `404 Artifact Not In Release` with code `artifact-not-in-release` when the release does not select
the requested source mapping. The package model's integrity and storage validation rules apply to this endpoint.
It returns `406 Not Acceptable` if the client does not accept `application/json`.

##### List source mapping packages

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/source-mappings/packages` lists the packages for the source
mappings whose resolved source schema has the logical artifact type `{artifactType}`. Each package contains one mapping's
metadata, optional condition program, all transformation programs, source schema, target schemas, and target store
identifiers.

The optional `sourceMappingArtifactType` filter restricts the collection to one mapping's package when it matches.
Without the filter, Data Ingestion retrieves all candidates' packages by following every page. Ingestion evaluates
conditions after retrieval and applies every matching mapping.

The response uses `application/json` and the common collection envelope. Each `items` entry MUST use the same source
mapping package model as the direct package endpoint. The shared model contains these fields:

| Field | JSON type | Description |
| --- | --- | --- |
| `schemaVersion` | string | Release used to resolve the source mapping; identical to the collection's `schemaVersion` when listed |
| `mapping` | object | Complete shared source mapping artifact model for the selected release |
| `schemas` | array | Source schema and every distinct transformation target, with complete metadata and parsed documents |
| `targetStores` | array | Distinct OCG store identifiers used by all transformation targets in this release |
| `programs` | array | Exact bytes and resource descriptors for the condition and every transformation program |
| `links` | object | Links to this package and the separate immutable resources |

Each package's `links.self` MUST point to `/v1/releases/{schemaVersion}/source-mappings/{artifactType}/package`.
Its `links.mapping` MUST point to `/v1/releases/{schemaVersion}/source-mappings/{artifactType}`.
Both links use the mapping's own logical artifact type, including when the package appears in a collection.
The embedded `mapping` uses the artifact model's direct `self` and `package` links.
Collection-level `links.self` continues to identify the requested collection, including its filters and pagination parameters.

Each `programs` item contains `name`, `role`, `mediaType`, `digest`, `size`, and `content`. Its `content` value uses
Base64 encoding from RFC 4648 section 4 for the exact stored bytes. The text MUST use canonical padding when required.
It MUST NOT contain whitespace or line breaks.

Base64 encoding allows the package to contain a program in any registered media type. It also preserves exact bytes for
digest verification. A client decodes each item's `content`, verifies `size` and `digest`, then parses or compiles the bytes
according to `mediaType` and the mapping's `engine` object.

Within each package, `mapping.programs` describes program targets. The package's `programs` array contains their exact
bytes. Every condition and transformation program `name` MUST match exactly one item's `name`. Every item MUST match the
resource descriptor in the immutable mapping revision. The array contains each referenced resource once, sorted by `name`.
An absent condition adds no condition item. Several transformations can reference the same resource without duplicating its
bytes.

Each `schemas` item contains `metadata` and `document`. `metadata` is the complete common artifact descriptor for the exact
schema revision. `document` is the parsed JSON Schema payload and can be an object or Boolean. The registry MUST preserve
the parsed value. Clients needing exact schema bytes use the metadata's payload link.

The array MUST contain the mapping's resolved source schema and every transformation's resolved target schema. It MUST
contain each distinct revision once. The source schema appears first, followed by targets sorted by `artifactType`, then
`artifactRevision`. Every embedded revision MUST match the mapping reference and the selected release. Multiple
transformations targeting the same schema share one schema entry.

The registry MUST return `500 Internal Server Error` with code `inconsistent-registry-state` if any returned package's
target lacks its required storage entry. The registry MUST NOT return a partial package or collection page after this failure.
A valid importer rejects such a release before the registry write.

For example, a matching `sourceMappingArtifactType` filter returns one package inside the collection:

```json
{
  "schemaVersion": "2.4.0",
  "items": [
    {
      "schemaVersion": "2.4.0",
      "mapping": {
        "schemaVersion": "2.4.0",
        "kind": "source-mapping",
        "artifactType": "example-cmdb-device",
        "artifactRevision": "01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "sourceSchema": {
          "artifactType": "example-cmdb-device",
          "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
          "metadata": "/v1/releases/2.4.0/schemas/example-cmdb-device",
          "content": "/v1/releases/2.4.0/schemas/example-cmdb-device/content"
        },
        "engine": {
          "language": "ocg-dsl",
          "languageVersion": "1.0.0",
          "executionProfile": "ocg-custom-transformations"
        },
        "programs": {
          "condition": {
            "name": "example-cmdb-device-condition.ocgdsl",
            "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-condition.ocgdsl"
          },
          "transformations": [
            {
              "name": "example-cmdb-device-transformation.json",
              "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-transformation.json",
              "targetSchema": {
                "artifactType": "device",
                "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
                "metadata": "/v1/releases/2.4.0/schemas/device",
                "content": "/v1/releases/2.4.0/schemas/device/content"
              },
              "cardinality": "one"
            },
            {
              "name": "example-cmdb-device-location.json",
              "href": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T/resources/example-cmdb-device-location.json",
              "targetSchema": {
                "artifactType": "located-in",
                "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6",
                "metadata": "/v1/releases/2.4.0/schemas/located-in",
                "content": "/v1/releases/2.4.0/schemas/located-in/content"
              },
              "cardinality": "one"
            }
          ]
        },
        "links": {
          "self": "/v1/releases/2.4.0/source-mappings/example-cmdb-device",
          "package": "/v1/releases/2.4.0/source-mappings/example-cmdb-device/package"
        }
      },
      "schemas": [
        {
          "metadata": {
            "kind": "schema",
            "artifactType": "example-cmdb-device",
            "artifactRevision": "01K4Q80PQ2J6R0A7KX9N3FHTVE",
            "schemaClass": "source-record"
          },
          "document": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$id": "https://schemas.example.org/resources/schema/example-cmdb-device/01K4Q80PQ2J6R0A7KX9N3FHTVE",
            "type": "object"
          }
        },
        {
          "metadata": {
            "kind": "schema",
            "artifactType": "device",
            "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
            "schemaClass": "entity-type"
          },
          "document": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$id": "https://schemas.example.org/resources/schema/device/01K4Q7S2YV4B5MC7B8G8DCDR5F",
            "type": "object"
          }
        },
        {
          "metadata": {
            "kind": "schema",
            "artifactType": "located-in",
            "artifactRevision": "01K4Q7T3ZW5C6ND8C9H9EDE4G6",
            "schemaClass": "edge-type"
          },
          "document": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$id": "https://schemas.example.org/resources/schema/located-in/01K4Q7T3ZW5C6ND8C9H9EDE4G6",
            "type": "object"
          }
        }
      ],
      "targetStores": [
        "data-center-inventory"
      ],
      "programs": [
        {
          "name": "example-cmdb-device-condition.ocgdsl",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+ocgdsl",
          "digest": "sha-256:2ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 348,
          "content": "dHJ1ZQ=="
        },
        {
          "name": "example-cmdb-device-location.json",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
          "digest": "sha-256:3ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 1024,
          "content": "eyJydWxlcyI6W119"
        },
        {
          "name": "example-cmdb-device-transformation.json",
          "role": "program",
          "mediaType": "application/vnd.apeiro.ocg.source-mapping-program.v1+json",
          "digest": "sha-256:1ca92891709e8c758f1ba20f86af44c2cb6540859f8b47d4b26184986f7f87a3",
          "size": 1584,
          "content": "eyIkc2NoZW1hIjoiaHR0cHM6Ly9zY2hlbWFzLmV4YW1wbGUub3JnLy4uLiJ9"
        }
      ],
      "links": {
        "self": "/v1/releases/2.4.0/source-mappings/example-cmdb-device/package",
        "mapping": "/v1/releases/2.4.0/source-mappings/example-cmdb-device",
        "descriptor": "/v1/artifacts/01K4Q82H6BP2R6M7MA9W5K3Q1T",
        "sourceMappings": "/v1/releases/2.4.0/source-mappings?sourceArtifactType=example-cmdb-device"
      }
    }
  ],
  "nextCursor": null,
  "links": {
    "self": "/v1/releases/2.4.0/schemas/example-cmdb-device/source-mappings/packages?sourceMappingArtifactType=example-cmdb-device"
  }
}
```

`mapping` and schema metadata are abbreviated. Actual responses MUST contain complete enriched mapping metadata, including
program digests and retrieval links, and complete schema descriptors. The program content strings illustrate Base64
encoding only; they abbreviate the program bytes and do not match the illustrative sizes or digests.

Each package resolves every program from its immutable mapping revision. It resolves all schemas from that mapping's
exact dependencies and all target stores from the selected release. The response `ETag` identifies the complete
collection page, including all returned packages and pagination metadata. Clients MUST verify each decoded program using
that item's `size` and `digest`. Clients needing exact descriptor bytes use the package's `links.descriptor`.

The package collection uses the same filter, pagination, and empty-result behavior as the metadata collection. It returns
`406 Not Acceptable` if the client does not accept `application/json`.

#### Store lookup

`GET /v1/releases/{schemaVersion}/schemas/{artifactType}/stores` returns the release-level storage assignment for one
logical schema. It does not return physical database names, connection data, or credentials.

```json
{
  "schemaVersion": "2.4.0",
  "schema": {
    "artifactType": "device",
    "artifactRevision": "01K4Q7S2YV4B5MC7B8G8DCDR5F",
    "schemaClass": "entity-type"
  },
  "stores": [
    "data-center-inventory"
  ],
  "links": {
    "self": "/v1/releases/2.4.0/schemas/device/stores",
    "schema": "/v1/releases/2.4.0/schemas/device"
  }
}
```

The `stores` array uses the order from the canonical manifest, which is ascending by store identifier. A canonical schema
has one or more stores. A `source-record` schema returns `200 OK` with an empty `stores` array because source records do
not have a direct OCG storage destination.

The resource returns `404 Schema Not In Release` only when the release does not select the requested schema. It MUST NOT
use `404` to represent a selected source-record schema with no storage assignment.

The path uses `stores` rather than `store` because a valid release can assign one canonical schema to several OCG stores.

#### Release-scoped URI and IRI resolution

`GET /v1/releases/{schemaVersion}/resolve?uri={uri}` resolves an exact key from the release manifest's `uriCatalog`.
The client MUST percent-encode the complete URI or Internationalized Resource Identifier (IRI) as one query value.

The registry MUST NOT apply case folding, Unicode normalization, path normalization, or scheme-specific equivalence. It
compares the decoded query value with the catalog key as an exact string.

For a primary payload, the server returns `307 Temporary Redirect` to `/v1/artifacts/{artifactRevision}`. For an
additional resource, it redirects to `/v1/artifacts/{artifactRevision}/resources/{resourceName}`. The response includes
`Schema-Version`, `Selected-Schema-Version`, `ETag`, and `Cache-Control: no-cache`. The entity tag covers the selected
catalog entry and redirect target. A matching `If-None-Match` produces `304 Not Modified` instead of a redirect.

The redirect target is immutable. The release-scoped resolver remains necessary because the same requested IRI can map
to another immutable revision in a later release. A key that is absent from the selected catalog produces
`404 URI Not In Release`.

#### Logical artifact history

`GET /v1/artifact-kinds/{kind}/{artifactType}/history` lists every release selection for one logical artifact. Results are
ordered from newest to oldest in activation-chain order. The resource supports cursor pagination and no other filters.

Each item contains `schemaVersion`, `artifactRevision`, `createdUnderSchemaVersion`, and links to the release-selected
descriptor and immutable revision metadata. Repeated revisions remain in the result. This allows a client to observe
reuse and rollback without inferring history from revision identifiers.

The resource returns `404 Logical Artifact Not Found` only if no release has selected the logical identity. A retired
artifact still has history and remains queryable.

#### Release comparison

`GET /v1/changes?from={oldVersion}&to={newVersion}` compares two immutable release manifests. Both parameters are
required. `from` MUST be an ancestor of `to` in the `previousRelease` chain.

The response contains `fromSchemaVersion`, `toSchemaVersion`, `compatibility`, `artifacts`, `schemaContextBindings`,
`uriCatalog`, and `storageCatalog`. Change entries use the same shapes and deterministic ordering as the manifest
`changes` object.

The server computes a net manifest difference. It does not concatenate intermediate change summaries. For example, an
artifact added and then removed between the selected endpoints does not appear in the net result.

The compatibility classifier follows the manifest rules. It MUST classify an unknown or unprovable effect as
`breaking`. The response also links to both release descriptors.

The server returns `400 Versions Not Comparable` when `from` is not an ancestor of `to`. It returns
`404 Release Not Found` when either version is unknown.

#### Conditional requests and integrity

Every successful `GET` or `HEAD` response MUST include an `ETag`. The server MUST evaluate `If-None-Match` for all
resource methods, including collection and byte resources.

Entity tags have these rules:

- An entity tag identifies the complete selected HTTP representation.
- Clients MUST treat every entity tag as opaque.
- The server MUST NOT assign the same strong entity tag to identity and gzip representations.
- Exact manifest and artifact byte identity responses SHOULD use the quoted content digest as a strong entity tag.
- A gzip response MAY use a strong entity tag only when it identifies the exact compressed bytes. It MUST NOT use the
  quoted digest of the uncompressed stored bytes alone as a strong entity tag.
- API-generated JSON MAY use a representation hash or an internal generation identifier.
- A schema version, artifact revision, and entity tag are not interchangeable identifiers.

The manifest and artifact `digest` fields verify stored bytes. `ETag` validates an HTTP representation. A generated JSON
representation can change without changing any artifact digest. Content coding can also change the entity tag without
changing the stored bytes or their digest.

A `304 Not Modified` response has no body. It MUST contain `Schema-Version`, `Cache-Control`, and the matched `ETag`. A
release-scoped `304` response also contains `Selected-Schema-Version`. The response MUST include
`Vary: Accept-Encoding` when the selected representation supports gzip.

#### Error responses

Error handling is carried out on two levels: on the protocol level (HTTP) and on the application level.

On the protocol level, the API MUST return an appropriate HTTP status code:

| Status | Use |
| --- | --- |
| `200 OK` | Successful metadata, collection, or byte response |
| `304 Not Modified` | The selected representation matches `If-None-Match` |
| `307 Temporary Redirect` | A release-scoped URI resolves to immutable artifact bytes |
| `400 Bad Request` | Invalid syntax, query, filter combination, cursor, or release comparison |
| `404 Not Found` | A release, artifact, schema, resource, or catalog key does not exist |
| `406 Not Acceptable` | The API cannot provide an acceptable representation |
| `410 Gone` | A mutable collection cursor expired |
| `429 Too Many Requests` | The client exceeded a read limit |
| `500 Internal Server Error` | The registry encountered an unexpected failure |
| `503 Service Unavailable` | No release is active or the registry cannot serve a consistent read |

A `429` or temporary `503` response SHOULD include `Retry-After`. Error responses MUST include `Schema-Version` when a
current release exists.

On the application level, the API MUST return a structured error response with media type `application/problem+json`
that contains the following attributes (wrapped by the `error` field):

- `code`: A technical code of the error situation to be used for support purposes (code is an error type, not a trace or
  correlation id).
- `message`: A stable, human-readable summary describing the error. It MUST NOT vary between occurrences except for
  localization.

The error structure MAY contain a `target` attribute that describes a data element (for example, a resource path).

If error processing requires nesting of error responses, it MUST use the `details` field for this purpose. The `details`
field MUST contain an array of JSON objects that have `code` and `message` properties with the same semantics as described
above.

```json
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{
  "error": {
    "code": "schema-not-in-release",
    "message": "Release 2.4.0 does not select schema unknown-device.",
    "target": "/v1/releases/2.4.0/schemas/unknown-device"
  }
}
```

##### Error codes

Version `v1` defines these error codes.

| Error Code | Status | Meaning and retry behavior |
| --- | --- | --- |
| `invalid-request` | `400` | A path value or request syntax is malformed. Retrying unchanged does not succeed. |
| `invalid-query-parameter` | `400` | A query parameter is unknown or malformed. Retrying unchanged does not succeed. |
| `invalid-filter-combination` | `400` | The supplied filters cannot be combined. Retrying unchanged does not succeed. |
| `unsupported-artifact-kind` | `400` | The requested artifact kind is not registered. Retrying unchanged does not succeed. |
| `unsupported-schema-class` | `400` | The requested schema classification is not registered. Retrying unchanged does not succeed. |
| `invalid-cursor` | `400` | The cursor is malformed or does not match the resource and filters. Retrying without that cursor can succeed. |
| `versions-not-comparable` | `400` | The `from` release is not an ancestor of the `to` release. Retrying unchanged does not succeed. |
| `release-not-found` | `404` | The requested schema version is unknown. Retrying can succeed only after that release is activated. |
| `artifact-not-in-release` | `404` | The release does not select the requested logical artifact. Retrying unchanged does not succeed. |
| `artifact-not-found` | `404` | The artifact revision or supplied artifact coordinates do not exist. Retrying unchanged does not succeed. |
| `resource-not-found` | `404` | The artifact does not contain the requested additional resource. Retrying unchanged does not succeed. |
| `schema-not-in-release` | `404` | The release does not select the requested logical schema. Retrying unchanged does not succeed. |
| `uri-not-in-release` | `404` | The release URI catalog does not contain the requested URI or IRI. Retrying unchanged does not succeed. |
| `logical-artifact-not-found` | `404` | No activated release has selected the requested logical artifact. Retrying can succeed after a future activation. |
| `not-acceptable` | `406` | The API cannot produce a representation accepted by the client. Retrying with a supported `Accept` value can succeed. |
| `cursor-expired` | `410` | A mutable collection cursor expired. Restarting pagination without the cursor can succeed. |
| `read-limit-exceeded` | `429` | The client exceeded a read limit. Retry according to `Retry-After` when present. |
| `inconsistent-registry-state` | `500` | Stored registry data violates an invariant. Retrying can succeed after the service repairs its state. |
| `internal-error` | `500` | An unexpected server failure occurred. Retrying can succeed after the failure is resolved. |
| `no-active-release` | `503` | No schema release is active. Retrying can succeed after release activation. |
| `registry-unavailable` | `503` | The registry cannot provide a consistent read. Retry according to `Retry-After` when present. |

The server MUST use the most specific error code from this catalog. A server can add error codes without changing the
API major version. Clients MUST handle an unknown error code through its HTTP status and standard error details members.

#### Deliberate version 1 exclusions

Version `v1` does not provide these capabilities:

- Schema authoring, approval, publication, or import operations.
- Arbitrary queries over JSON Schema properties or JSON-LD context terms.
- Server-side JSON Schema compatibility evaluation.
- Mutable paths such as `/v1/schemas/{artifactType}` that silently select the current release.
- A release bundle download before the project defines deterministic archive bytes.

These exclusions keep the initial contract deterministic and read-only. A later API version can add query capabilities
without changing immutable release or artifact resources.
