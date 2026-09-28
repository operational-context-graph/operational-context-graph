# Container Image Hardening — Doris FE, Doris BE, PostgreSQL

## Purpose

This document explains why the Telemetry Store and Inventory Store require hardened container images, what hardening is applied, and how to build and use those images. It is the companion to the Dockerfiles in this folder.

---

## Why hardening is required

### Apache Doris (FE and BE)

The upstream `apache/doris:fe-4.1.4` and `be-4.1.4` images are based on **Ubuntu 22.04** and carry a significant vulnerability surface that must be reduced before production use.

**Critical advisories in shippable components (must be fixed before production):**

| Component | Advisory | Where |
|-----------|----------|-------|
| Bouncy Castle | CVE in bundled JAR | FE — Hadoop client on the FE classpath |
| Go toolchain | Outdated version | FE — embedded in `dorisctl` and `doris-debug` auxiliary binaries |

**High advisories (dozens):**

| Surface | Examples |
|---------|---------|
| Bundled Java libraries | netty, jackson, jetty, thrift |
| Ubuntu base-OS kernel tooling | gcc, g++, binutils, gdb, strace, linux-headers |

These are *non-structural* — none prevent Doris from working correctly. They are resolved by removing unnecessary components and upgrading bundled dependencies.

Apache does not publish an official slim or Alpine Doris image, so the Ubuntu 22.04 base is the only upstream option. The hardening approach here (remove tooling, drop debug binaries, run as non-root) reduces the surface without a base-image change.

**Local development** uses the upstream images directly (`apache/doris:fe-4.1.4`, `be-4.1.4`) for speed — no build step needed to get started.

**Production** MUST use the hardened images built from `infrastructure/doris/Dockerfile.fe` and `Dockerfile.be`.

### PostgreSQL

The official `postgres:18` image is based on **Debian Trixie** and ships with build tooling (gcc, g++, binutils, make) and debug utilities (gdb, strace, ltrace) not needed at runtime.

**Preferred alternative: `postgres:18-alpine`**

An Alpine Linux variant (`postgres:18-alpine`) is available and has a significantly smaller base surface than the Debian image — Alpine ships almost no extra tooling by default, making the `apt-get remove` step below largely unnecessary. Use the Alpine variant unless a dependency on glibc is confirmed (Alpine uses musl libc).

For this project, the Debian variant is used because no glibc incompatibility has been identified. Switching to Alpine is a straightforward change (update the `FROM` line, remove the `apt-get` block).

---

## Hardening approach

All three images follow the same pattern:

### 1. Pin the upstream image digest in production

Image tags are mutable — `postgres:18` today is not the same bytes as `postgres:18` tomorrow. In production, replace the tag with the immutable digest:

```dockerfile
# Instead of:
FROM apache/doris:fe-4.1.4

# Use:
FROM apache/doris:fe-4.1.4@sha256:<digest>
```

Obtain the digest after pulling:
```bash
docker inspect --format='{{index .RepoDigests 0}}' apache/doris:fe-4.1.4
docker inspect --format='{{index .RepoDigests 0}}' apache/doris:be-4.1.4
docker inspect --format='{{index .RepoDigests 0}}' postgres:18
```

### 2. Remove build and debug tooling

In each Dockerfile:
```dockerfile
RUN apt-get update --quiet && \
    apt-get remove -y --auto-remove \
        gcc g++ cpp binutils gdb strace ... \
        2>/dev/null || true && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*
```

`--auto-remove` removes packages installed only as dependencies of the removed packages, narrowing the surface further. `|| true` prevents build failure when a package is absent in a future base image update.

### 3. Remove auxiliary binaries (Doris FE only)

`dorisctl` embeds an outdated Go toolchain (Critical advisory). `doris-debug` is a diagnostic binary not needed in production. Both are removed from the FE image:

```dockerfile
RUN rm -f \
    /opt/apache-doris/fe/bin/dorisctl \
    /opt/apache-doris/fe/bin/doris-debug
```

This eliminates the Critical Go toolchain advisory entirely on the FE image.

### 4. Run as non-root

All three images run the database process as a non-root user (`doris` for Doris, `postgres` for PostgreSQL). The upstream images already create these users; the Dockerfiles make it explicit with a final `USER` directive.

Running as root inside a container is not a defence boundary — it is an escalation path if container escape is achieved.

### 5. Bake in configuration (optional in dev, required in production)

The project's configuration files are copied into the image so it is self-contained:

| Image | Config baked in |
|-------|----------------|
| `doris/Dockerfile.fe` | `infrastructure/doris/fe.conf` |
| `doris/Dockerfile.be` | `infrastructure/doris/be.conf` |
| `postgres/Dockerfile` | `infrastructure/postgres/postgresql.conf`, `pg_hba.conf` |

In docker-compose (local dev), these files are mounted as volumes instead — the image uses the upstream tag and no build step is needed. In production, the baked-in config makes the image self-contained without volume dependencies.

---

## Remaining surface (production prerequisites)

These items require engineering work beyond simple package removal:

| Item | Image | Resolution |
|------|-------|------------|
| Bouncy Castle JAR | Doris FE | Replace the bundled Hadoop client JAR with a patched version, or remove it if HDFS is not used |
| Java library advisories (netty, jackson, jetty, thrift) | Doris FE + BE | Identify JAR paths in the image and replace with patched versions during the build |
| Ubuntu base-OS surface | Doris FE + BE | Rebuild on a minimal base image once upstream Doris supports it |

---

## Building the hardened images

```bash
# Build hardened Doris FE
docker build \
  -f infrastructure/doris/Dockerfile.fe \
  -t apeiro/doris-fe:4.1.4-hardened \
  .

# Build hardened Doris BE
docker build \
  -f infrastructure/doris/Dockerfile.be \
  -t apeiro/doris-be:4.1.4-hardened \
  .

# Build hardened PostgreSQL
docker build \
  -f infrastructure/postgres/Dockerfile \
  -t apeiro/postgres:18-hardened \
  .
```

---

## Using hardened images in docker-compose

Local dev uses the upstream images — no build step needed. To test with hardened images locally, build them first then update `docker-compose.yaml`:

```yaml
# Before (local dev — upstream image):
doris-fe:
  image: apache/doris:fe-4.1.4

# After (hardened):
doris-fe:
  image: apeiro/doris-fe:4.1.4-hardened
```

---

## Doris BE host prerequisites

These are host-level requirements that cannot be set inside the container. The BE will refuse to start without them:

```bash
# Required sysctl — set on the Docker host, not inside the container
sudo sysctl -w vm.max_map_count=2000000

# Disable swap
sudo swapoff -a
```

In Kubernetes, configure these via an init container or a privileged DaemonSet. The Doris Operator handles this automatically when managing Doris clusters.
