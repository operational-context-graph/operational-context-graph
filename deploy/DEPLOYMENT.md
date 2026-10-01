---
label: OCG Local Development
toc: true
---

# OCG Local Development

## What is in this folder

```
deploy/
  compose/
    dev.yml              Docker Compose for local development
  sql/
    doris-init.sql       Doris DDL — ocg database + logs, metrics, traces tables
    doris-users.sql      Doris users — ocg_writer, ocg_reader
    postgres-init.sql    PostgreSQL schema — assets, relationships, asset_history
```

## Prerequisites

- Docker Desktop (macOS/Windows) or Docker Engine (Linux)
- Go 1.27+
- `mysql`

---

## Local development

### Start the full stack

From the repo root:

```bash
task infra:up        # start Postgres + Doris (~60s for Doris to become healthy)
task schema:all      # apply schemas and create Doris users (one-time)
task build:services  # build inventory-store and telemetry-store images
task services:up     # start all services
```

### Services

| Service | Address |
|---|---|
| Inventory Store | http://localhost:8080 |
| Telemetry Store | http://localhost:8081 |
| PostgreSQL | localhost:5432 |
| Doris FE HTTP | http://localhost:8030 |
| Doris FE MySQL | localhost:9030 (MySQL client) |
| Adminer (DB browser) | http://localhost:8090 |

### Adminer login

**PostgreSQL:**

| Field | Value |
|---|---|
| System | PostgreSQL |
| Server | `postgres` |
| Username | `inventory` |
| Password | `dev-password-not-for-production` |
| Database | `inventory` |

**Doris:**

| Field | Value |
|---|---|
| System | MySQL |
| Server | `doris-fe:9030` |
| Username | `ocg_reader` |
| Password | `dev-password-not-for-production` |
| Database | `ocg` |

### Tear down

```bash
task infra:down      # stop all services and delete all data volumes
```

---

## Container images

Images are published to the GitHub Container Registry (GHCR) at `ghcr.io/operational-context-graph/<name>`.

### Hardened infrastructure images

The `infrastructure/` directory contains hardened Dockerfiles for Postgres and Doris. These are built and pushed automatically by `release.yml` on every tag push (e.g. `v1.2.3`).

To build manually from the repo root:

```bash
task build:infra
```

### Application service images

```bash
task build:services        # builds inventory-store and telemetry-store
task build:all             # builds all images (infra + services)
```

---

## Production deployment

Production deployment (OCM component descriptor, Helm chart, or equivalent) is tracked in a separate ticket and depends on the OCM infrastructure ticket. It is not part of this folder.
