## v1.0.0 (2026-11-09)

### Features

- *(DevOps)* Dockerfile refactoring ([#132](https://github.com/operational-context-graph/operational-context-graph/pull/132))
- *(DevOps)* Add semantic release pr creation workflows ([#120](https://github.com/operational-context-graph/operational-context-graph/pull/120))
- *(Data Ingestion)* Implement BackfillService with gap-state watermark tracking ([#93](https://github.com/operational-context-graph/operational-context-graph/pull/93))
- Add database liveness check to readyz ([#88](https://github.com/operational-context-graph/operational-context-graph/pull/88))
- *(Data Ingestion)* Hardwire schemas ([#64](https://github.com/operational-context-graph/operational-context-graph/pull/64))
- Add inventory-store services ([#32](https://github.com/operational-context-graph/operational-context-graph/pull/32))
- *(Telemetry Store)* Telemetry store service + fix store writers in data ingestion ([#48](https://github.com/operational-context-graph/operational-context-graph/pull/48))
- Data ingestion telemetry and inventory store writer ([#49](https://github.com/operational-context-graph/operational-context-graph/pull/49))
- *(Data Ingestion)* Data ingestion outbox drain ([#53](https://github.com/operational-context-graph/operational-context-graph/pull/53))
- Add native OTLP gRPC receiver ([#40](https://github.com/operational-context-graph/operational-context-graph/pull/40))
- Data ingestion pipeline ([#43](https://github.com/operational-context-graph/operational-context-graph/pull/43))
- Implement IngestionService gRPC handler with mTLS and config validation ([#30](https://github.com/operational-context-graph/operational-context-graph/pull/30))
- Data ingestion scaffold ([#20](https://github.com/operational-context-graph/operational-context-graph/pull/20))

### Bug Fixes

- *(Dependencies)* Update module github.com/go-playground/validator/v10 to v10.30.6 ([#118](https://github.com/operational-context-graph/operational-context-graph/pull/118))
- *(DevOps)* Add gomodTidyAll to postUpdateOptions in renovate.json ([#119](https://github.com/operational-context-graph/operational-context-graph/pull/119))
- *(Dependencies)* Update gomod non-major dependencies ([#111](https://github.com/operational-context-graph/operational-context-graph/pull/111))
- *(Dependencies)* Update module google.golang.org/grpc to v1.85.0 ([#95](https://github.com/operational-context-graph/operational-context-graph/pull/95))
- Handle ErrUnavailable in OTLP receiver in Data Ingestion ([#87](https://github.com/operational-context-graph/operational-context-graph/pull/87))
- *(DevOps)* Use Go version from initialized Go workspace in all CI workflows ([#83](https://github.com/operational-context-graph/operational-context-graph/pull/83))
- Extensions in inventory-store and isRelationship in data-ingestion after end-to-end demo flow test ([#76](https://github.com/operational-context-graph/operational-context-graph/pull/76))
- Add asset name field and correct octobus log timestamp format ([#67](https://github.com/operational-context-graph/operational-context-graph/pull/67))
- Correct data extraction for OTLP data ([#66](https://github.com/operational-context-graph/operational-context-graph/pull/66))
- *(Dependencies)* Update module github.com/knadh/koanf/v2 to v2.4.1 ([#56](https://github.com/operational-context-graph/operational-context-graph/pull/56))
- *(Dependencies)* Update module github.com/knadh/koanf/providers/env to v2 ([#27](https://github.com/operational-context-graph/operational-context-graph/pull/27))

### Miscellaneous Tasks

- *(DevOps)* Consolidate CI workflow to support go monorepo ([#84](https://github.com/operational-context-graph/operational-context-graph/pull/84))
- *(DevOps)* Remove SPDX headers from various files ([#94](https://github.com/operational-context-graph/operational-context-graph/pull/94))
- Simplify data-ingestion config ([#89](https://github.com/operational-context-graph/operational-context-graph/pull/89))
- Remove dead code in data-ingestion ([#86](https://github.com/operational-context-graph/operational-context-graph/pull/86))
- Add label to Dockerfile and update Readme ([#34](https://github.com/operational-context-graph/operational-context-graph/pull/34))

