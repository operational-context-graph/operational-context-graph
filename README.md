[![Continuous Integration](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/ci.yml) [![CodeQL Advanced](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml/badge.svg)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml) [![Vulnerability Scan](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/vulnerability-scan.yml/badge.svg?branch=main)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/vulnerability-scan.yml) [![OSS Compliance Validation](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/oss-compliance.yml/badge.svg?branch=main)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/oss-compliance.yml) [![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/operational-context-graph/operational-context-graph/badge)](https://scorecard.dev/viewer/?uri=github.com/operational-context-graph/operational-context-graph) [![REUSE status](https://api.reuse.software/badge/github.com/operational-context-graph/operational-context-graph)](https://api.reuse.software/info/github.com/operational-context-graph/operational-context-graph)

# Operational Context Graph

## About this project

Stores and serves all operational data for a data center. Provides telemetry, inventory, runbooks, recorded actions, and a live semantic knowledge graph through a single access-controlled API.

## Repository layout

This is a polyglot monorepo. Sub-components live under `services/` (long-running servers and batch connectors under `services/connectors/`), `shared/`, and `frontend/`. The gRPC interface definitions are the source of truth for the API and live under `proto/`.

## Requirements and Setup

### Prerequisites

- [Go](https://go.dev/dl/) — version defined in `go.work` (currently 1.27.3)
- [Buf](https://buf.build/docs/installation) — for protobuf code generation
- [pnpm](https://pnpm.io/installation) — for OpenSpec tooling

### Getting started

This repository uses a [Go workspace](https://go.dev/ref/mod#workspaces) (`go.work`) to manage all Go modules together. The workspace and its checksum file (`go.work.sum`) are committed — no extra setup step is needed.

```bash
# Clone and enter the repo
git clone https://github.com/operational-context-graph/operational-context-graph.git
cd operational-context-graph

# Install Node dependencies (OpenSpec tooling)
pnpm install

# Generate protobuf stubs
make proto-generate

# Build all services
make build

# Run all tests
make test
```

### Adding a new Go service

1. Create your module under `services/` with its own `go.mod`.
2. Add it to the `use` block in `go.work`.
3. Run `go work sync` to update `go.work.sum`.
4. If your module requires a newer Go version than the current workspace minimum, update the `go` directive in `go.work` to match.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/operational-context-graph/operational-context-graph/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](https://github.com/operational-context-graph/.github/blob/main/CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/operational-context-graph/.github/blob/main/SECURITY.md) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/operational-context-graph/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors. Please see our [LICENSE](./LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/operational-context-graph/operational-context-graph).
