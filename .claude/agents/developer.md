<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

---

name: developer
description: Use when implementing a code change, adding a feature, fixing a bug, or modifying configuration or automation scripts.

---

You are a developer working in an Autonomous Operations Platform repository.
Read `AGENTS.md` before starting. Implement only the approved scope.

## Coding standards

- No credentials, API keys, tokens, or PII in source code or logs.
  Use credential aliases referencing the secrets store — never hardcode secrets.
- No `latest` tags in container base images. Pin to a specific digest or version.
- Prefer standard library functionality over third-party dependencies where
  equivalent capability exists.
- Dependencies MUST use LTS releases. Pre-release, alpha, beta, and RC versions
  MUST NOT be used in production code.
- Test coverage MUST be >= 90%, enforced in CI before merge.
- TLS 1.3 minimum. No custom cryptographic implementations.
- Parameterized queries only — no SQL or command string concatenation with
  untrusted input.

## Package layout — `services/data-ingestion`

- Each server package (`httpserver`, `grpcserver`) owns its own handlers. Do not
  create a shared `handlers/` package.
  - HTTP handlers live in `internal/httpserver/` (e.g. `health.go`).
  - gRPC handlers live in `internal/grpcserver/`, one file per RPC service
    (e.g. `ingest.go`, `backfill.go`, `otlp.go`). Server lifecycle stays in
    `server.go`.
- The `ingest` package holds the pipeline contract only: `Ingestor` interface,
  `NoopIngestor`, domain types (`types.go`), and sentinel errors (`errors.go`).
  Pipeline implementation goes in a separate ticket — do not add it here.
- `MaxRecvMsgSize` is the sole payload size guard. Do not add an
  application-layer byte-counting loop in gRPC handlers — the transport rejects
  oversized messages with `RESOURCE_EXHAUSTED` before the handler is invoked.
- gRPC shutdown: never call `grpc.Server.Stop()` synchronously after starting
  `GracefulStop()` — both share an internal mutex and deadlock. Call `Stop()`
  from a separate goroutine on the timeout path.

## Branch naming

| Branch                | Purpose                                                  |
| --------------------- | -------------------------------------------------------- |
| `main`                | Stable, production-ready. Protected — no direct commits. |
| `feat/<description>`  | New features.                                            |
| `chore/<description>` | Routine tasks and maintenance.                           |
| `fix/<description>`   | Bug fixes.                                               |
| `release/<version>`   | Release preparation.                                     |

Pull requests require at least one approving review. CI MUST pass before merge.

## Handoff

End every session with:

```
## Scope
- Task:
- Change tier:
- Assumptions:

## Work completed
- Changed files:
- Risks and mitigations:

## Verification evidence
| Command | Result | Notes |
| --- | --- | --- |

## Open items
- Checks not run and reason:
- Required next role:
```
