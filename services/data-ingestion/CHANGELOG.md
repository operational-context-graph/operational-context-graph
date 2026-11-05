## [services/data-ingestion/v1.0.0] - 2026-11-05

### Features

- *(devops)* Add semantic release pr creation workflows ([#120](https://github.com/operational-context-graph/backlog/issues/120))
- *(di)* Implement BackfillService with gap-state watermark tracking ([#93](https://github.com/operational-context-graph/backlog/issues/93))
- Add database liveness check to readyz ([#88](https://github.com/operational-context-graph/backlog/issues/88))
- *(di)* Hardwire schemas ([#64](https://github.com/operational-context-graph/backlog/issues/64))
- Add inventory-store services ([#32](https://github.com/operational-context-graph/backlog/issues/32))
- *(ts)* Telemetry store service + fix store writers in data ingestion ([#48](https://github.com/operational-context-graph/backlog/issues/48))
- Data ingestion telemetry and inventory store writer ([#49](https://github.com/operational-context-graph/backlog/issues/49))
- *(di)* Data ingestion outbox drain ([#53](https://github.com/operational-context-graph/backlog/issues/53))
- Add native OTLP gRPC receiver ([#40](https://github.com/operational-context-graph/backlog/issues/40))
- Data ingestion pipeline ([#43](https://github.com/operational-context-graph/backlog/issues/43))
- Implement IngestionService gRPC handler with mTLS and config validation ([#30](https://github.com/operational-context-graph/backlog/issues/30))
- Data ingestion scaffold ([#20](https://github.com/operational-context-graph/backlog/issues/20))

### Bug Fixes

- *(deps)* Update module github.com/go-playground/validator/v10 to v10.30.6 ([#118](https://github.com/operational-context-graph/backlog/issues/118))
- *(devops)* Add gomodTidyAll to postUpdateOptions in renovate.json ([#119](https://github.com/operational-context-graph/backlog/issues/119))
- *(deps)* Update gomod non-major dependencies ([#111](https://github.com/operational-context-graph/backlog/issues/111))
- *(deps)* Update module google.golang.org/grpc to v1.85.0 ([#95](https://github.com/operational-context-graph/backlog/issues/95))
- Handle ErrUnavailable in OTLP receiver in Data Ingestion ([#87](https://github.com/operational-context-graph/backlog/issues/87))
- *(devops)* Use Go version from initialized Go workspace in all CI workflows ([#83](https://github.com/operational-context-graph/backlog/issues/83))
- Extensions in inventory-store and isRelationship in data-ingestion after end-to-end demo flow test ([#76](https://github.com/operational-context-graph/backlog/issues/76))
- Add asset name field and correct octobus log timestamp format ([#67](https://github.com/operational-context-graph/backlog/issues/67))
- Correct data extraction for OTLP data ([#66](https://github.com/operational-context-graph/backlog/issues/66))
- *(deps)* Update module github.com/knadh/koanf/v2 to v2.4.1 ([#56](https://github.com/operational-context-graph/backlog/issues/56))
- *(deps)* Update module github.com/knadh/koanf/providers/env to v2 ([#27](https://github.com/operational-context-graph/backlog/issues/27))

### Miscellaneous Tasks

- *(devops)* Consolidate CI workflow to support go monorepo ([#84](https://github.com/operational-context-graph/backlog/issues/84))
- *(devops)* Remove SPDX headers from various files ([#94](https://github.com/operational-context-graph/backlog/issues/94))
- Simplify data-ingestion config ([#89](https://github.com/operational-context-graph/backlog/issues/89))
- Remove dead code in data-ingestion ([#86](https://github.com/operational-context-graph/backlog/issues/86))
- Add label to Dockerfile and update Readme ([#34](https://github.com/operational-context-graph/backlog/issues/34))

