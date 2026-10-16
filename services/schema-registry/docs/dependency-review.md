# Dependency Review

The Schema Registry validates untrusted release metadata. These libraries are
selected before implementation starts. Callers supply bounded, in-memory bytes.
No library receives a network, file, or environment resolver.

| Purpose | Module and version | License | Maintenance and security policy | Network behavior | Advisory status | Replacement path |
| --- | --- | --- | --- | --- | --- | --- |
| JSON Schema Draft 2020-12 | `github.com/santhosh-tekuri/jsonschema/v6` `v6.0.3` | BSD-3-Clause | Active versioned upstream releases. Security reports use GitHub private reporting. | The registry supplies an in-memory compiler loader. No remote URL loader is configured. | `govulncheck` is required before merge. | Replace behind the artifact schema-validation interface. |
| JSON-LD | `github.com/piprate/json-gold` `v0.9.1` | Apache-2.0 | Versioned GitHub releases. Security reports use GitHub security reporting. | The registry supplies a document loader that resolves only release-catalog entries. | `govulncheck` is required before merge. | Replace behind the artifact context-validation interface. |
| RFC 8785 canonicalization | `github.com/gowebpki/jcs` `v1.0.2` | Apache-2.0 | Versioned upstream releases. Security reports use GitHub security reporting. | Canonicalization operates only on caller-owned JSON bytes. | `govulncheck` is required before merge. | Replace behind the manifest canonicalization interface. |

The initial integration imports the libraries without activating loaders. Phase
two adds bounded format validation and explicit deny-by-default loaders.
