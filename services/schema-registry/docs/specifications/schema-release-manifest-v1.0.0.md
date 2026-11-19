# Schema Release Manifest Specification v1.0.0

This document contains the specification for the _Schema Release Manifest_ describing a schema release compatible
with the _Schema Registry_ of the _Operational Context Graph_.

# Schema artifact identifiers

A _logical artifact_ is a named data-model definition that continues to exist across schema releases. The logical
artifact is  not one stored file or one immutable revision. It represents the concept whose definition changes over
time.

A _logical artifact type_ is the stable registry name for that logical artifact. Consumers use it to ask questions
such as:

- What is the `device` entity schema in release `2.4.0`?
- Which revisions of the `location` entity schema have existed?
- Which JSON-LD context is associated with the `device` schema?
- Which schemas refer to the `depends-on` edge schema?

All schema artifacts contained in a schema release MUST have a logical artifact type.

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

- A schema release MUST use lowercase ASCII logical artifact types that consist only of alphanumeric characters,
  i.e., match the regular expression `[a-z0-9]+(?:-[a-z0-9]+)*`. Types are case-sensitive and require no path encoding.
- A schema release MUST not reuse a logical artifact type after retirement. Reuse could cause historical references
  to appear to refer to a different concept.

## Schema artifacts in a release

The Schema Registry stores a number of schema artifact kinds. Together, they define the canonical data model used by the
OCG and explain how source data relates to that model.

- _JSON schemas_ define the structure and constraints of data models.
- _JSON-LD contexts_ map canonical properties to external vocabulary IRIs.
- _Source mappings_ transform source records into canonical records.

All schema artifact kinds are immutable and versioned. The _Schema Release Manifest_ selects the exact revision of each
schema artifact that belongs to one data schema version.

## Schema release manifest

The _Schema Release Manifest_ is the main consistency and synchronization resource for a schema release. It describes a
complete release without embedding every document.

The manifest is a JSON document. Version 1 uses the media type `application/vnd.apeiro.ocg.schema-release-manifest.v1+json`.

The following sections define version `1.0.0` of the manifest format. A publisher MUST reject a candidate manifest that
does not satisfy this specification. The _Schema Registry Release Importer_ validates the release manifest before it
stores any release data.

### Format principles

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

The release manifest MUST NOT contain its own digest because that would create a self-reference.

### Top-level object

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

### Previous release

`previousRelease` binds the release history chain to exact manifest bytes. A schema version alone is insufficient
because it does  not detect an invalid reassignment of that version.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `schemaVersion` | string | Yes | Schema version of the immediate predecessor |
| `manifestDigest` | string | Yes | SHA-256 digest of the predecessor manifest bytes |

The first published release MUST use `null`. Every later release MUST identify the current release at publication time
as  its immediate predecessor. A rollback still creates a new schema version and points to the release from which the
rollback occurs.

### Release source

`source` identifies the authoritative Git snapshot from which a _Schema Authoring Environment_ published the release.
Review, approval, and pipeline evidence remains in that environment and is not duplicated in the release manifest.

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `repository` | string | Yes | Absolute URI of the Git repository |
| `commit` | string | Yes | Full lowercase hexadecimal Git object ID |

The commit MUST exist in the repository and contain the release source. The repository and commit provide the lookup key
for full provenance in the Schema Authoring Environment.

### Artifact entries

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
`[a-z0-9]+(?:-[a-z0-9]+)*`. `artifactRevision` is opaque to consumers. A registry can use a
[Universally Unique Lexicographically Sortable Identifier (ULID)](https://github.com/ulid/spec), but consumers MUST NOT
depend on that representation or derive time from  it.

An artifact revision binds `kind`, `artifactType`, `schemaClass`, `targetSchemaVersion`, `canonicalUri`,
`createdUnderSchemaVersion`, payload media type and content, every resource name, role, media type and content, and all
dependencies. These values MUST remain identical wherever a release reuses the revision. `bundlePath` is release
packaging metadata. A publisher can change it without creating a new artifact revision, but it MUST identify the bound
bytes in the release bundle.

`createdUnderSchemaVersion` remains unchanged when a later release reuses the revision. It MUST equal the earliest
release  in which the revision appears. `targetSchemaVersion` MUST identify this release or one of its ancestors.
A source mapping can be reused only when all its target dependencies select the same revisions and compatibility
validation succeeds against the current release. This rule permits safe reuse without hiding a change in the target
model.

`schemaClass` is required only for `schema` entries. Values come from the Schema Registry's classification catalog. The
initial classifications include `entity-type`, `identifier-type`, `edge-type`, and `source-record`. Consumers MUST treat
unknown classifications as unsupported rather than guessing their semantics.

The primary payload of a `schema` entry MUST use `application/schema+json`. This requirement gives schema consumers one
unambiguous media type for validation documents.

`canonicalUri` MUST identify this exact revision. It MUST NOT be a `current` alias or another URI whose result can
change. Two artifact entries MUST NOT use the same canonical URI. The registry MUST reject a canonical URI that any
historical revision already uses for a different revision.

#### Byte descriptors

`payload` and every additional resource use content digests. A payload descriptor contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `mediaType` | string | Yes | Media type of the exact bytes |
| `digest` | string | Yes | SHA-256 digest of the exact bytes |
| `size` | integer | Yes | Number of bytes, greater than or equal to zero |
| `bundlePath` | string | Yes | Path of the same bytes in an offline release bundle |

Clients retrieve a primary payload through the artifact revision resource of the _Schema Introspection API_. They
retrieve an additional resource through that artifact revision and the resource name. `bundlePath` identifies the same
bytes in an offline release bundle. It MUST use `/` separators. It MUST NOT start with `/`, contain an empty segment,
or contain `.` or `..` segments. Every bundle path in a manifest MUST be unique.

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

The descriptor and all referenced programs form one immutable source mapping revision. Changing a condition, a
transformation, or a target reference creates a new revision. Program resources do not create separate logical
artifacts.

Several source mapping entries MAY resolve `sourceSchema` to the same schema logical artifact type and selected revision.
Each source mapping MUST retain its own unique logical artifact type. Publishers and importers MUST NOT reject source
mappings solely because they share a source schema or have overlapping conditions.

#### Artifact dependencies

`dependencies` makes cross-artifact references discoverable without parsing each artifact language. Each dependency
contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Target artifact kind |
| `artifactType` | string | Yes | Target logical artifact type |
| `artifactRevision` | string | Yes | Exact target revision in this release |

Every dependency target MUST occur in `artifacts` with the same kind, logical type, and revision. A publisher MUST
include every external reference that affects validation, expansion, or mapping. The publisher derives dependencies
from the artifact content and removes duplicate targets. Relationship semantics remain in the artifact content and in
dedicated manifest structures such as `schemaContextBindings`. Internal references within one payload do not create
dependency entries. A publisher MUST reject hidden network dependencies that are neither selected artifacts nor
additional resources.

### Schema-to-context bindings

`schemaContextBindings` states which JSON-LD contexts a consumer applies to a specific schema revision. The binding uses
revisions because a binding to a logical type would change meaning when a later release selects another revision.

Each binding contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `schemaRevision` | string | Yes | Revision of a `schema` entry |
| `contextRevisions` | array | Yes | One or more revisions of `context` entries |

Every referenced revision MUST occur in `artifacts` with the required kind. A schema revision can occur in at most one
binding. A context can occur in several bindings. The context order is not semantically significant.

### URI and IRI catalog

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

### Storage catalog

`storageCatalog` tells ingestion components which OCG stores accept records governed by a canonical schema. Source-record
schemas that only describe input data MUST NOT have a storage entry. Unmapped source data must never reach an OCG store.

Each storage entry contains:

| Field | JSON type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | Yes | Artifact kind; version 1 requires `schema` |
| `artifactType` | string | Yes | Logical schema type |
| `stores` | array | Yes | One or more registered OCG store identifiers |

The pair `(kind, artifactType)` MUST occur once in `storageCatalog`. It MUST identify a schema class that governs
canonical records. Every schema that governs canonical records MUST have one storage entry. A source-record schema
MUST NOT have a storage entry. The catalog intentionally uses logical identity instead of an artifact revision. Store
placement is a release-level association. A publisher or importer MUST reject a manifest that assigns a target store
to a source-record schema. Such an assignment identifies a malformed release manifest. Changing only a valid store
association still creates a new schema release.

Release manifest format revision `1.0.0` supports these store identifiers:

| Store | Identifier |
| --- | --- |
| Data Center Inventory | `data-center-inventory` |
| Telemetry Store | `telemetry-store` |
| Insights Repository | `insights-repository` |
| Recorded Actions Repository | `recorded-actions-repository` |

A revision `1.0.0` release manifest MUST use only these identifiers. The manifest MUST NOT contain physical database
names, connection data, or deployment-specific credentials.

### Deterministic change summary

`changes` is optional. A release author does not need to provide a change summary. When present, its machine-readable
fields record the exact difference from `previousRelease`. The publication pipeline computes those fields. The optional
`summary` field is human-authored and can originate from the release description in the Schema Authoring Environment.

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

The classifier MUST treat an unknown or unprovable effect as `breaking`. Removing an artifact is breaking. Adding a
new logical artifact is additive unless its bindings or storage placement change existing interpretation. Removing or
renaming a type or property is breaking. Adding an optional type or property is additive. A documentation correction or
equivalent mapping metadata change is operational. Removing a binding, URI mapping, or storage mapping is breaking.
Adding one is additive unless it changes the interpretation or destination of existing data, in which case it is
breaking. Updating a URI mapping is breaking. A source mapping behavior change is breaking unless validation in the
Schema Authoring Environment establishes that all previously valid inputs have equivalent canonical outputs.

Source mapping behavior includes condition selection and the combined output of all applicable mappings and
transformations. Adding a mapping for an existing source schema is not automatically additive. Changing a condition
can change which canonical records ingestion produces, even when every target schema remains unchanged.

When the first release contains `changes`, all change arrays MUST be empty and compatibility MUST be `initial`. For later
releases that contain `changes`, the change arrays MUST exactly equal a comparison with the manifest identified by
`previousRelease`. The textual `summary` is descriptive and is not part of this comparison. A release with no effective
difference MUST NOT be published.

### Ordering and canonical bytes

The publisher serializes the release manifest with the JSON Canonicalization Scheme (JCS) from
[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785). Any SHA-256 digest of the manifest MUST cover the resulting UTF-8
bytes. The canonical document has no byte order mark and no trailing newline. Parsers MUST reject duplicate object
member names before canonicalization.

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

UTF-8 is the only byte encoding for the manifest. The UTF-16 rule above defines string comparison only. It does not
define the release manifest's byte encoding.

### Digest and identifier syntax

Release manifest metadata uses these common lexical forms:

| Value | Required form |
| --- | --- |
| SHA-256 digest | `sha-256:` followed by 64 lowercase hexadecimal characters |
| Schema version | `0.0.0` or a higher `MAJOR.MINOR.PATCH` value without leading zeroes |
| Artifact revision | One or more RFC 3986 unreserved characters; values `.` and `..` are forbidden |
| Logical artifact type | `[a-z0-9]+(?:-[a-z0-9]+)*` |
| Store and role | `[a-z0-9]+(?:-[a-z0-9]+)*` |
| Git commit | Full lowercase hexadecimal object ID; 40 or 64 characters |

The `sha-256:` spelling applies to Schema Registry JSON metadata. It intentionally differs from the `sha256:` spelling
used by e.g. Open Container Initiative (OCI) descriptors.

### Cross-field validation

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

### Complete example

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
