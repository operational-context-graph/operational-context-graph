# OCM Bundle

This directory holds all OCM-related artifacts for the Operational Context Graph (OCG).

## Contents

| File | Committed | Purpose |
| --- | --- | --- |
| `config` | Yes | OCM v2 config — signing, credentials (key path, not material), transfer settings |
| `ocg.pub.pem` | Yes | RSA public key used to verify OCG bundle signatures |
| `component-constructor.yaml` | Yes | OCM component descriptor — 10 resources (5 images + 2 dep charts + chart + bundle-manifest + image-values) |
| `README.md` | Yes | This file |
| `ocg.key` | **No** (gitignored) | RSA private key — never committed; registered as CI secret `OCM_SIGNING_KEY` |

## Local build

```bash
# Prerequisites: ocm >= 0.22.1 (v2), helm, docker/crane, yq, openssl
# Private key must be at /tmp/ocg-signing-key.pem (or set SIGN_KEY=<path>)

VERSION=1.2.3 SIGN_KEY=/path/to/ocg.key make ocm-build
VERSION=1.2.3 SIGN_KEY=/path/to/ocg.key make package
VERSION=1.2.3 SIGN_KEY=/path/to/ocg.key make inspect
VERSION=1.2.3 SIGN_KEY=/path/to/ocg.key make sign
VERSION=1.2.3 SIGN_KEY=/path/to/ocg.key make verify
```

Local builds are for testing only. CI is the only path that publishes to the registry.

## Recovery: partial registry push

**Symptom:** `ocm transfer` was interrupted and the component version in the registry is incomplete.

**Diagnosis:** Run `ocm verify component-version --signature ocg-sig <registry-ref>`. If it exits non-zero, the push is incomplete or corrupt.

**Recovery:**

1. Manually delete the partial component version from the registry using the registry's UI or API. The component version identifier is `github.com/operational-context-graph/operational-context-graph:<version>`.
2. Re-run the CI publish job (re-trigger the `release.yml` workflow run or create a new system release).
3. Verify `ocm verify component-version --signature ocg-sig <registry-ref>` exits 0 after re-publish.

**Note:** The "fail if version exists" check in `release.yml` assumes any existing version is complete. A partial push from a prior interrupted run will not be detected by this check alone — use `ocm verify` as the signal.

## Signing keypair

The keypair was generated with:

```bash
openssl genrsa -out .ocm/ocg.key 4096
openssl rsa -in .ocm/ocg.key -pubout -out .ocm/ocg.pub.pem
```

The public key is committed (`ocg.pub.pem`). The private key (`ocg.key`) is gitignored and MUST be registered as the CI secret `OCM_SIGNING_KEY` before the release pipeline can sign bundles.

To register the key:
1. Base64-encode the private key: `base64 -w0 .ocm/ocg.key`
2. Add the output as a repository secret named `OCM_SIGNING_KEY` in GitHub Settings > Secrets.
3. Store a secure backup of the private key outside the repository.
