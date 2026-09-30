# OCG Local Development

---

## What is in this folder

```
deploy/
  compose/
    dev.yml              Docker Compose for local development (upstream images)
```

---

## Local development (Docker Compose)

> **Note:** The application images (`inventory-store`, `telemetry-store`, `data-ingestion`) do not exist yet.
> Until they are built and published, only the infrastructure services (Postgres, Doris) can be started locally.

### Start infrastructure only

From the repo root:

```bash
task infra:up
```

Or directly with Docker Compose:

```bash
docker compose -f deploy/compose/dev.yml up sysctl-init postgres doris-fe doris-be
```

`vm.max_map_count` is set automatically — no manual host configuration needed on macOS.
On Linux, remove `sysctl-init` from the command and run `sudo sysctl -w vm.max_map_count=2000000` on the host instead.

### Start full stack (once application images exist)

```bash
docker compose -f deploy/compose/dev.yml up
```

### Services

| Service         | Address                        |
|-----------------|--------------------------------|
| Inventory Store | http://localhost:8080          |
| Telemetry Store | http://localhost:8081          |
| PostgreSQL      | localhost:5432                 |
| Doris FE HTTP   | http://localhost:8030          |
| Doris FE MySQL  | localhost:9030 (MySQL client)  |

### Tear down

```bash
task infra:down          # stop and delete all data volumes
```

Or directly:

```bash
docker compose -f deploy/compose/dev.yml down      # stop, keep data volumes
docker compose -f deploy/compose/dev.yml down -v   # stop and delete all data
```

---

## Container images

Images are published to the GitHub Container Registry (GHCR) at `ghcr.io/operational-context-graph/<name>`.

### Hardened infrastructure images

The `infrastructure/` directory contains hardened Dockerfiles for Postgres and Doris.
These are built and pushed automatically by `release.yml` on every tag push (e.g. `v1.2.3`).

To build manually from the repo root:

```bash
docker build -f infrastructure/postgres/Dockerfile   -t ghcr.io/operational-context-graph/postgres:latest .
docker build -f infrastructure/doris/Dockerfile.fe   -t ghcr.io/operational-context-graph/doris-fe:latest .
docker build -f infrastructure/doris/Dockerfile.be   -t ghcr.io/operational-context-graph/doris-be:latest .
```

To push manually (requires `docker login ghcr.io` with a PAT that has `write:packages`):

```bash
docker push ghcr.io/operational-context-graph/postgres:latest
docker push ghcr.io/operational-context-graph/doris-fe:latest
docker push ghcr.io/operational-context-graph/doris-be:latest
```

### Application service images (placeholders)

`inventory-store`, `telemetry-store`, and `data-ingestion` images will be built from
`services/<name>/Dockerfile` once those services are implemented.
The `release.yml` workflow already contains the placeholder jobs — remove the `if: false`
condition in each job when the Dockerfile exists.

---

## Production deployment

Production deployment (OCM component descriptor, Helm chart, or equivalent) is tracked in a separate ticket
and depends on the OCM infrastructure ticket. It is not part of this folder.
