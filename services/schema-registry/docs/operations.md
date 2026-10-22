<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

# Schema Introspection API Operations

## Module and workspace

`services/schema-registry` is one Go module. The root `go.work` registers it
with `services/data-ingestion`. The Schema Introspection API command is
`cmd/schema-introspection-api`.

The module keeps format validation, shared models, PostgreSQL reads, and HTTP
transport in separate packages. `internal/schema-artifacts` validates immutable
artifact, source-mapping, and manifest formats. `internal/storage/postgres`
owns read-only database access. `internal/introspection` owns API services and
transport. The API process never imports Data Ingestion packages.

## Contract and limits

The service exposes only versioned `GET` and `HEAD` resources under `/v1`.
`/healthz` and `/readyz` remain outside that prefix. The API has no mutation
method, release-import, publication, or activation endpoint.

| Resource | Limit |
| --- | ---: |
| Exact manifest, payload, or resource | 16 MiB |
| Generated identity JSON | 64 MiB |
| JSON nesting | 64 levels |
| Object members | 256 |
| Array or format collection entries | 10,000 |
| JSON string | 1 MiB UTF-8 bytes |
| Identifier or media type | 255 UTF-8 bytes |
| URI, decoded cursor, or decoded `uri` | 8 KiB |
| Request target | 16 KiB |
| Header field / aggregate headers | 8 KiB / 32 KiB |

Artifact payloads use strict I-JSON. Schemas use JSON Schema Draft 2020-12.
Contexts use the profiled JSON-LD media type. Release manifests use RFC 8785
JSON Canonicalization Scheme bytes and UTF-16 code-unit ordering. Source-
mapping program resources remain opaque. The service MUST NOT parse, compile,
execute, or certify program bytes.

Schema and JSON-LD resolvers accept only the selected release closure. They
MUST NOT read network, file, environment, or authoring-repository resources.

## Conformance corpus

Manifest conformance fixtures live in
`internal/schema-artifacts/manifest/testdata/`. `corpus.json` identifies each
fixture and its expected result. The format-package tests load this corpus and
also exercise strict parsing, artifact validation, source-mapping validation,
offline resolver canaries, and canonical serialization. Run the format and
route conformance command in the verification table after changing a format or
transport contract.

## Configuration and secrets

Start from `configs/schema-introspection-api.example.yaml`. The loader applies
built-in defaults, optional YAML, and `OCG_SCHEMA_INTROSPECTION_` environment
overrides in that order. Supply database credentials through a secret-backed
environment or mounted secret. Do not store them in this repository.

The configuration validates positive timeouts, a positive `/v1` admission
limit, reserved readiness capacity, verified PostgreSQL Transport Layer
Security (TLS) before listening. Production PostgreSQL connections require peer
and hostname verification.

Pagination cursors are opaque, unsigned, unpadded Base64url-encoded JSON. They
do not expire and have no signing-key or rotation configuration. The service
validates every decoded value, request binding, and activated snapshot before
use. A valid caller-constructed cursor receives the same visibility and service
limits as a server-issued cursor. Tokens provide no cryptographic tamper
detection.

## Storage, trust, and recovery

Goose migrations under `migrations/` create a passive PostgreSQL read model.
The runtime role has only `CONNECT`, schema `USAGE`, and required `SELECT`
grants. It has no migration, data-definition, sequence, function, temporary
table, or data-mutation privilege.

Release state, activation ordering, reachability, and the current pointer are
pre-populated inputs. The API reads them in one repeatable-read snapshot. It
MUST NOT create, repair, infer, transition, or activate release state.

Database and migration administrators are trusted to preserve coherent state.
They MUST retain administrative audit logs, encrypt volumes and backups, monitor
retention, test point-in-time recovery, and verify a restore before production
exposure. The API does not perform backup, restore, encryption, or audit work.
`docs/storage-security.md` records these platform-owned controls.

Recovery disables or rolls back the API deployment while retaining immutable
registry data. Restore PostgreSQL with the platform's tested point-in-time
recovery procedure. Apply migrations with the separate migration principal
before returning the API to service. Verify the restored read model with the
module migration integration tests and API conformance targets below.

## Health and draining

`/healthz` returns `200` when the HTTP process can serve requests. `/readyz`
uses reserved PostgreSQL capacity and a bounded query. It returns `200` when
the database is reachable, even if no release is active.

On `SIGTERM`, the process first marks readiness unavailable. It then rejects
new `/v1` admission, cancels in-flight work, waits for the configured shutdown
deadline, closes the pool, and force-closes remaining transports. Readiness and
draining behavior are covered by `internal/introspection/httpapi` race tests.

## Scope exclusions

Authentication and authorization are deferred. Their absence is not a security
finding within this component's current scope. The upstream load balancer owns
inbound HTTPS/TLS termination and certificate management. The service receives
HTTP after that termination.

Schema release activation is fully excluded. This includes commands, APIs,
migration triggers, transition guards, writer interfaces, runtime helpers, and
test-only activation helpers.

## Scoped verification

Run commands from `services/schema-registry`.

| Purpose | Command | Evidence |
| --- | --- | --- |
| Build the API | `make build` | Builds `schema-introspection-api`. |
| Run unit and integration tests | `make test` | Runs `go test ./... -race`. |
| Inspect coverage | `make coverage` | Writes `coverage/schema-registry.out`. |
| Static analysis | `make vet` | Runs `go vet ./...`. |
| Lint | `make lint` | Runs golangci-lint or `go vet`. |
| Dependency integrity | `go mod verify` | Verifies downloaded module content. |
| Vulnerability analysis | `make vuln` | Runs `govulncheck`. |
| Format and route conformance | `go test ./internal/schema-artifacts/... ./internal/introspection/httpapi -race` | Exercises strict formats, offline resolvers, HTTP routing, errors, and draining. |
| Migration integration | `go test ./internal/storage/postgres -race` | Uses the PostgreSQL integration harness when Docker is available. |
| Run locally | `make run-schema-introspection-api` | Uses runtime configuration and read-only credentials. |

The restoration procedure is platform-owned. Record each production restore
drill with its platform test evidence before approving production exposure.
