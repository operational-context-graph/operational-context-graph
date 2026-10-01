[![CodeQL Advanced](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml/badge.svg)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml) [![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/operational-context-graph/operational-context-graph/badge)](https://scorecard.dev/viewer/?uri=github.com/operational-context-graph/operational-context-graph) [![REUSE status](https://api.reuse.software/badge/github.com/operational-context-graph/ocg)](https://api.reuse.software/info/github.com/operational-context-graph/ocg)

# Operational Context Graph

## About this project

Stores and serves all operational data for a data center. Provides telemetry, inventory, runbooks, recorded actions, and a live semantic knowledge graph through a single access-controlled API.

### Local development

From the repo root:

```bash
# 1. Start infrastructure (Postgres + Doris)
task infra:up

# 2. Apply schemas and create Doris users (one-time)
task schema:all

# 3. Build service images
task build:services

# 4. Start all services
task services:up
```

Verify:

```bash
curl http://localhost:8080/healthz   # inventory-store
curl http://localhost:8081/healthz   # telemetry-store
```

### Services

| Service | Address |
|---|---|
| Inventory Store | http://localhost:8080 |
| Telemetry Store | http://localhost:8081 |
| PostgreSQL | localhost:5432 |
| Doris FE HTTP | http://localhost:8030 |
| Doris FE MySQL | localhost:9030 |
| Adminer (DB browser) | http://localhost:8090 |

### Tear down

```bash
task infra:down   # stop all services and delete all data volumes
```

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/operational-context-graph/repository-template/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](https://github.com/operational-context-graph/.github/blob/main/CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/operational-context-graph/.github/blob/main/SECURITY.md) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/operational-context-graph/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors. Please see our [LICENSE](./LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/operational-context-graph/repository-template).
