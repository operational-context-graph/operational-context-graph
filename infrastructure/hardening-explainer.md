# Container Image Hardening

## Container image

A container image is a packaged snapshot of everything a service needs to run — the operating system, the software, and its dependencies. When you deploy to Kubernetes, each pod runs from one of these images.

---

## Why hardening?

The official images published by Apache and PostgreSQL are built for general use. They include tools useful during development and debugging — compilers, debuggers, build utilities — that are not needed when the software is simply running in production.

Hardening means taking an official image and removing everything that is not needed at runtime. The result is a smaller, more locked-down image. If an attacker were to break out of the container, they would find far fewer tools available to do damage.

---

## Why does it matter?

Every tool left in an image is a potential weapon. A compiler can build new malicious binaries. A debugger can inspect running processes. A known-vulnerable library can be exploited directly.

Security scanners (such as Trivy) detect these and report them as advisories with severity levels:

- **Critical** — a vulnerability in a component that is actively loaded or executable in the running container. Must be fixed before production.
- **High** — a vulnerability in a bundled component that is present but not directly on the main execution path. Should be fixed before production.

---

## The three images we harden

### 1. Apache Doris FE (Frontend)

**What it is:** The Doris Frontend manages metadata, query planning, and the HTTP endpoints Data Ingestion writes to.

**Base OS:** Ubuntu 22.04 — chosen by Apache, not by us. Apache does not publish a slim variant, so this is the only option.

**Critical advisories found:**

| Advisory | What it is | Why it is Critical |
|---|---|---|
| Bouncy Castle JAR | A Java cryptography library bundled as part of the Hadoop client on the FE classpath. The specific version has a known CVE. | It is on the active classpath of the running FE process — it is loaded every time the FE starts. |
| Outdated Go toolchain | `dorisctl` and `doris-debug` are auxiliary management and diagnostic binaries compiled with an outdated version of Go that has known vulnerabilities. | They are executable files on disk inside a running container. They are never used in production but they are present and exploitable if access is gained. |

**What we do:**
- Remove build and debug tooling: gcc, g++, cpp, binutils, gdb, strace, linux-headers
- Delete `dorisctl` and `doris-debug` entirely — this completely eliminates the Go toolchain Critical advisory
- Run as non-root `doris` user
- Bake in `fe.conf` so the image is self-contained

**What still needs an owner before production:**
- Replace the Bouncy Castle JAR with a patched version, or remove the Hadoop client if HDFS is not used
- Upgrade bundled Java library JARs: netty, jackson, jetty, thrift

---

### 2. Apache Doris BE (Backend)

**What it is:** The Doris Backend handles compute and storage — it receives data from Stream Load, writes to disk, and serves query results.

**Base OS:** Ubuntu 22.04 — same as FE, same reason.

**Advisories found:**
- Dozens of High advisories in bundled Java libraries (jackson, etc.) and Ubuntu kernel tooling (gcc, g++, binutils, gdb, strace, linux-headers)
- No Critical advisories specific to the BE (the Go toolchain advisories are FE-only)

**What we do:**
- Remove build and debug tooling: gcc, g++, cpp, binutils, gdb, strace, linux-headers
- Run as non-root `doris` user
- Bake in `be.conf`

**What still needs an owner before production:**
- Upgrade bundled Java library JARs
- Rebuild on a minimal base image once upstream Doris supports it

---

### 3. PostgreSQL

**What it is:** The database backing the Inventory Store — stores assets and topology relationships.

**Base OS:** Debian Bookworm — chosen by the PostgreSQL team, not by us.

**Version chosen:** `postgres:16` — we actively chose this version because it has the longest support horizon of the current stable releases (LTS until November 2028), making it the most stable production target.

**Advisories found:**
- Build tooling present in the base image: gcc, g++, binutils, make
- Debug utilities present: gdb, strace, ltrace
- None of these are needed at runtime

**What we do:**
- Remove build and debug tooling: gcc, g++, cpp, binutils, make, gdb, strace, ltrace
- Bake in `postgresql.conf` and `pg_hba.conf` — enforces scram-sha-256 auth and production settings
- Run as non-root `postgres` user (UID 999)
- Digest-pin the image in production so the tag cannot change silently between builds

**Alpine alternative:** `postgres:16-alpine` has an even smaller surface by default because Alpine ships almost no extra tooling. It is a straightforward switch unless a glibc dependency is confirmed (Alpine uses musl libc instead).

---

## Dev vs production rule

| Environment | Images to use | Build step needed? |
|---|---|---|
| Local development / demo | Upstream images: `apache/doris:fe-4.1.4`, `be-4.1.4`, `postgres:16` | No |
| Production | Hardened images: `apeiro/doris-fe:4.1.4-hardened`, `apeiro/doris-be:4.1.4-hardened`, `apeiro/postgres:16-hardened` | Yes — build from the Dockerfiles in this folder |

**Digest pinning in production:** image tags are mutable — `postgres:16` today is not guaranteed to be the same bytes as `postgres:16` tomorrow. In production, pin each image to its immutable SHA256 digest so builds are reproducible and no silent substitution can occur.

---

## Step-by-step: what happens when we harden an image

1. Start from the upstream image (`FROM postgres:16`)
2. Switch to root user to make changes (`USER root`)
3. Remove packages not needed at runtime (`apt-get remove gcc g++ ...`)
4. Remove specific binaries not needed at runtime (Doris FE only: `dorisctl`, `doris-debug`)
5. Copy production configuration files into the image
6. Switch back to the non-root service user (`USER postgres` / `USER doris`)
7. Build and tag the hardened image (`apeiro/postgres:16-hardened`)
8. In production: replace the tag with the immutable digest

---

## What is still open

| Item | Image | What is needed |
|---|---|---|
| Bouncy Castle JAR | Doris FE | Replace with a patched JAR or remove the Hadoop client if HDFS is unused |
| Java library advisories (netty, jackson, jetty, thrift) | Doris FE + BE | Identify JAR paths and replace with patched versions during the build |
| Ubuntu base-OS surface | Doris FE + BE | Rebuild on a minimal base image once upstream Doris supports it |
| Telemetry Store SDD | — | Add hardened image names, digest pinning, dev vs production table, and decision record — tracked in `telemetry-store-sdd-hardening-notes.md` |
