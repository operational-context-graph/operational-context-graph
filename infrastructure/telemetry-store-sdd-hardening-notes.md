# Hardening points to include in the Telemetry Store SDD

## What is already covered in the SDD

| Content | Location |
|---|---|
| Critical advisories (Bouncy Castle, Go toolchain) and High advisories (netty, jackson, jetty, thrift, Ubuntu tooling) | §10.2, R4 |
| Image hardening flagged as a production prerequisite needing an owner | §10.2 R4, Q7 |
| Host prerequisites (vm.max_map_count, swap disabled) | §10.4 |

---

## What is missing and should be added

### 1. Container layout table (§10.3 or §10.2)

Split the image reference into dev and production rows, as done in the Inventory Store SDD:

| Component | Dev / demo image | Production image |
|---|---|---|
| Doris FE | `apache/doris:fe-4.1.4` | `apeiro/doris-fe:4.1.4-hardened` |
| Doris BE | `apache/doris:be-4.1.4` | `apeiro/doris-be:4.1.4-hardened` |

The SDD currently references Doris images without distinguishing upstream from hardened.

### 2. Hardened image content (§10.2)

State explicitly what the hardened images do:
- Remove build and debug tooling: gcc, g++, cpp, binutils, gdb, strace, linux-headers
- Remove `dorisctl` and `doris-debug` from the FE image — this eliminates the Critical Go toolchain advisory entirely
- Run as non-root `doris` user (explicit `USER doris` directive)
- Bake in `fe.conf` / `be.conf` so the image is self-contained in production

### 3. Digest pinning (§10.2)

Add one sentence: in production, replace the mutable tag with an immutable digest:

```bash
docker inspect --format='{{index .RepoDigests 0}}' apache/doris:fe-4.1.4
docker inspect --format='{{index .RepoDigests 0}}' apache/doris:be-4.1.4
```

Then use `FROM apache/doris:fe-4.1.4@sha256:<digest>`.

### 4. Dev vs production table (§10.2 or new subsection)

Add a row for the image to the dev vs production differences — same pattern as the Inventory Store SDD:

| Setting | Dev / demo | Production | Risk if not changed |
|---|---|---|---|
| Doris FE image | `apache/doris:fe-4.1.4` (upstream) | `apeiro/doris-fe:4.1.4-hardened`; digest-pinned | Upstream tag is mutable; Critical/High advisories present; debug tooling present |
| Doris BE image | `apache/doris:be-4.1.4` (upstream) | `apeiro/doris-be:4.1.4-hardened`; digest-pinned | Same |

### 5. New decision record

Add a decision record (e.g. TS-DEC-005 or similar, following the existing numbering) covering:
- **Context:** which Doris version to pin and whether to use upstream or hardened images
- **Options:** upstream tag, hardened image, digest-pinned hardened image
- **Outcome:** hardened + digest-pinned for production; upstream acceptable for dev/demo only; build step required before production deployment
- **Consequence:** production deployments require a build step; `dorisctl` / `doris-debug` removal eliminates the Critical Go toolchain advisory on the FE image; remaining surface (Bouncy Castle, Java library advisories, Ubuntu base-OS) still requires an owner (Q7)

---

## What should NOT be added

- The specific `apt-get remove` package list — implementation detail; belongs in the Dockerfile only
- Build commands — belong in `hardening.md` only
- docker-compose swap examples — belong in `hardening.md` only
