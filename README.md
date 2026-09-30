[![CodeQL Advanced](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml/badge.svg)](https://github.com/operational-context-graph/operational-context-graph/actions/workflows/codeql.yml) [![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/operational-context-graph/operational-context-graph/badge)](https://scorecard.dev/viewer/?uri=github.com/operational-context-graph/operational-context-graph) [![REUSE status](https://api.reuse.software/badge/github.com/operational-context-graph/ocg)](https://api.reuse.software/info/github.com/operational-context-graph/ocg)

# Operational Context Graph

## About this project

Stores and serves all operational data for a data center. Provides telemetry, inventory, runbooks, recorded actions, and a live semantic knowledge graph through a single access-controlled API.

## Requirements and Setup

### Local development (infrastructure only)

Requires: Docker Desktop (macOS/Windows) or Docker Engine (Linux).

From the repo root:

```bash
docker compose -f deploy/compose/dev.yml up sysctl-init postgres doris-fe doris-be
```

`vm.max_map_count` is set automatically — no manual host configuration needed. Doris takes ~60s to become healthy.

Verify:
```bash
curl http://localhost:8030/api/bootstrap  # Doris FE — expect {"msg":"success",...}
```

Tear down:
```bash
docker compose -f deploy/compose/dev.yml down      # keep data
docker compose -f deploy/compose/dev.yml down -v   # delete all data
```

> The application services (`inventory-store`, `telemetry-store`, `data-ingestion`) are not yet implemented. See [deploy/DEPLOYMENT.md](deploy/DEPLOYMENT.md) for the full deployment guide including Kubernetes/Helm instructions.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/operational-context-graph/repository-template/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](https://github.com/operational-context-graph/.github/blob/main/CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/operational-context-graph/.github/blob/main/SECURITY.md) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/operational-context-graph/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors. Please see our [LICENSE](./LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/operational-context-graph/repository-template).
