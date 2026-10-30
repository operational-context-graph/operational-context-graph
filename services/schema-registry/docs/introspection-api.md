# Schema Introspection API

## Scope

The Schema Registry exposes read-only HTTP version `v1` through `GET` and `HEAD`.
Read `/v1/registry` first. Use its schema version for every related release-scoped request.
`Schema-Version` identifies the current release. `Selected-Schema-Version` identifies the requested release.
Release-scoped JSON contains its selected `schemaVersion`.

This is the first, unreleased implementation. The initial migration and fixtures use only the current descriptor format.
There is no legacy descriptor reader, singular mapping route, import writer, or activation mechanism.
Authentication and authorization remain deferred. The platform terminates inbound Transport Layer Security (TLS).
PostgreSQL peer verification and read-only database privileges remain required.

## Mapping identity and discovery

A source schema's logical artifact type and a mapping's logical artifact type are independent identifiers.
Several mappings can accept one source schema revision. Conditions can overlap.
The Registry lists every candidate without evaluating conditions or selecting a winner.
Consumers retrieve every candidate page before evaluating conditions and applying every matching mapping.

All paths below start with `/v1/releases/{schemaVersion}`.

| Suffix | Result |
| --- | --- |
| `/source-mappings` | Paginated catalog of complete enriched mapping objects |
| `/source-mappings/{mappingType}` | One complete enriched mapping object |
| `/source-mappings/{mappingType}/package` | One complete package object |
| `/schemas/{sourceType}/source-mappings` | Paginated metadata for the selected source schema revision |
| `/schemas/{sourceType}/source-mappings/packages` | Paginated packages for that source schema revision |

The catalog accepts exact `sourceArtifactType`, `targetArtifactType`, `engineLanguage`, and `executionProfile` filters.
Filters combine with logical AND. Schema filters compare resolved dependencies, not descriptor text alone.
A target filter matches any transformation. It returns each whole mapping once, including transformations targeting other schemas.
Unknown source or target schemas return `404 schema-not-in-release`.

Both nested collections accept exact `sourceMappingArtifactType`, comparing the mapping's own logical artifact type.
A mapping associated with another source schema does not match that filter.
An unknown, well-formed mapping type returns an empty collection.
Malformed, empty, duplicated, oversized, or unknown query parameters return `400 invalid-query-parameter`.

All collections contain `schemaVersion`, `items`, `nextCursor`, and `links.self`.
Zero, one, and several matches retain this envelope.
No matches return `200`, empty `items`, and `nextCursor: null`.
An absent direct mapping returns `404 artifact-not-in-release`.
An unknown or inactive release returns `404 release-not-found`.

Collections accept `limit` from 1 through 1000, defaulting to 100.
Pass each `nextCursor` unchanged with the same resource, source schema, release, filters, and limit.
Cursors are unsigned, opaque, unpadded Base64url tokens. They do not expire and provide no cryptographic tamper detection.
The server validates cursor structure, values, request bindings, and visible snapshots.
Metadata and package collection cursors cannot substitute for each other.

## Complete mapping model

Direct metadata bodies equal their corresponding catalog and nested collection items.
Each package's `mapping` field uses this same complete object.

Every mapping includes the following common artifact descriptor fields:

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Selected release |
| `kind` | `source-mapping` |
| `artifactType` | Mapping's own logical artifact type |
| `artifactRevision` | Immutable revision identifier |
| `createdUnderSchemaVersion` | First release selecting this revision |
| `targetSchemaVersion` | Canonical release targeted by every transformation |
| `canonicalUri` | Immutable primary payload identifier |
| `payload` | `mediaType`, `digest`, `size`, and immutable `href` |
| `resources` | Complete resource descriptors containing `name`, `role`, `mediaType`, `digest`, `size`, and `href` |
| `dependencies` | Exact `kind`, `artifactType`, `artifactRevision`, and metadata `href` references |
| `extensions` | Optional namespaced metadata copied from the manifest |
| `links` | Direct `self`, immutable `content`, logical `history`, and direct `package` links |

The enriched object also includes these fields:

| Field | Meaning |
| --- | --- |
| `sourceSchema` | Resolved source `artifactType`, `artifactRevision`, and selected-release `content` link |
| `engine` | Publisher-declared `language`, `languageVersion`, and `executionProfile` |
| `programs.condition` | Optional complete program resource descriptor |
| `programs.transformations` | Non-empty ordered transformation list |

An absent condition is omitted, not represented by null or a synthetic program.
Each transformation contains all six resource fields, its `cardinality`, and a resolved `targetSchema`.
Target references use the same three fields as `sourceSchema`.
Cardinality is `one` or `many` for that transformation, not for the mapping as a whole.
Transformation order matches the stored descriptor. Shared programs do not remove transformation entries.

Stored descriptors use `programRef` and `programDigest`. API program metadata uses `name` and `digest` instead.
Each program binds one mapping-owned resource by exact name, role `program`, and digest.
Every program resource is referenced. Repeated references share one resource.
Mapping revisions MAY also own ancillary resources with non-program roles.
Ancillary resources remain in descriptor metadata, with immutable retrieval links.
Packages do not embed ancillary bytes or count them toward Base64 program content.
There is no top-level `targetSchema`, `cardinality`, or singular `program` in the enriched object.

All release-scoped mapping objects use direct mapping-identity `self` and `package` links.
This includes mapping descriptors returned by generic artifact endpoints.
Schema descriptors expose plural `sourceMappings`, pointing to the catalog's `sourceArtifactType` filter.
Immutable revision metadata remains release-independent and omits package links.

## Complete package model

Direct package bodies equal corresponding nested package collection items.
Every package contains the following fields:

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Selected release, also matching the containing collection when listed |
| `mapping` | Complete enriched mapping metadata described above |
| `schemas` | Source schema and every distinct target revision |
| `targetStores` | Sorted distinct union of every target's registered store identifiers |
| `programs` | Every referenced condition and transformation resource, including exact bytes |
| `links` | Direct package `self`, direct `mapping`, and immutable `descriptor` byte links |

Each schema item contains complete common artifact `metadata` and parsed JSON Schema `document`.
The document preserves object or boolean values. Exact schema bytes remain available through metadata payload links.
Schemas appear once per revision: source first, then targets ordered by logical type and revision.

Each program item contains `name`, `role`, `mediaType`, `digest`, `size`, and `content`.
Content uses RFC 4648 section 4 Base64 with canonical padding and no whitespace.
Program items appear once per resource, sorted by name. An absent condition contributes no byte item.
Zero-byte programs use an empty Base64 string and the matching zero size and digest.

Consumers decode every program and verify both byte length and SHA-256 digest before interpreting it.
The Registry does not parse, compile, execute, or certify condition or transformation programs.
Engine names, profiles, and program media types do not imply Registry execution support.

## Integrity, limits, and failures

Stored payloads and resources have a 16 MiB limit.
Descriptor arrays have a 10,000-entry limit. Conditions and transformations obey the same strict JSON rules as other artifacts.
Generated identity JSON has a 64 MiB limit, including metadata, documents, escaping, Base64 expansion, and collection envelopes.
The page limit applies to returned items, not the extra pagination lookahead item.
Format validation rejects candidates requiring oversized individual packages before storage.

Missing target storage, inconsistent projections, corrupt bytes, or oversized stored representations return `500 inconsistent-registry-state`.
A database failure or timeout returns `503 registry-unavailable`.
Assembly completes before success commitment. A later program, target, or package failure returns no partial object or page.

Every representation supports conditional requests, identity encoding, and gzip.
Entity tags identify complete objects or pages, including pagination metadata.
Identity and gzip use distinct strong validators. `HEAD` returns negotiated headers without a body.
All responses use `Cache-Control: no-cache`. A `304` still reports the current schema version.

Adding a mapping for an existing source schema is conservatively breaking when equivalent combined output remains unproven.
Conditions and the combined output of all applicable mappings affect compatibility, even with unchanged target schemas.
The comparison API never proves equivalence by executing programs.

## Verification

Run these commands from `services/schema-registry`:

```sh
make build
make vet
make lint
make test
```

Focused race-enabled verification uses the same Make target:

```sh
make test TEST_PACKAGES='./internal/schema-artifacts/manifest ./internal/storage/postgres'
make test TEST_PACKAGES=./internal/introspection/httpapi
make test TEST_PACKAGES=./internal/introspection/httpapi TEST_ARGS='-run=NoSuchTest -bench=BenchmarkMultiTargetPackagePage -benchtime=1x'
```

`mapping_contract_test.go` compares complete direct bodies and collection items using integrity-consistent program bytes.
It also verifies schema and program deduplication, target-store unions, filters, cursors, snapshots, and whole-page size failures.
`mapping_regressions_test.go` verifies request-scoped schema lookup counts, ancillary resources, and release lookup errors.
PostgreSQL tests validate the initial migration, shared sources, resource references, ordered projections, read-only grants, and corruption detection.

See [operations](operations.md), [storage security](storage-security.md), and [dependency review](dependency-review.md) for existing operational controls.
