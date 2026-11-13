package manifest

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// TestValidateHistoricalRevisionAndURIBindings rejects identity rebinding and historical URI
// reassignment independently of current canonical paths and supporting-document media types.
// Requested context aliases remain release-scoped when historical bytes prove context-only use.
func TestValidateHistoricalRevisionAndURIBindings(t *testing.T) {
	for _, changeKind := range []bool{false, true} {
		m, c := populatedRelease(t)
		old := m.Artifacts[1]
		newSelection := old.Metadata()
		newSelection.Type = "replacement"
		if changeKind {
			newSelection.Kind, newSelection.SchemaClass = artifact.KindContext, ""
		}
		require.ErrorContains(t, indexHistory([]Release{{Manifest: m}}).validateReuse(newSelection), "immutable revision changed")
		// Catalog alias reassignment must fail independently of canonical path validation.
		next := m
		next.Artifacts = append([]artifact.Artifact(nil), m.Artifacts...)
		next.Artifacts[1].Revision = "rev-2"
		next.Artifacts[1].CanonicalURI += "-new"
		next.Artifacts[1].Payload.Digest = digest([]byte("false"))
		next.URICatalog = append([]URIEntry(nil), m.URICatalog...)
		for i := range next.URICatalog {
			if next.URICatalog[i].ArtifactRevision == old.Revision {
				next.URICatalog[i].ArtifactRevision = "rev-2"
			}
		}
		require.ErrorContains(t, next.validateHistoricalURIs(Context{History: []Release{{Manifest: m}}}), "historical immutable URI")
		require.NoError(t, m.Validate(c))
	}
	for _, test := range []struct {
		name, media, before, after, evidence, want, pointer string
		context, payload                                    bool
	}{
		{name: "generic schema", media: "application/json", before: `{"$id":"https://example.test/support","type":"string"}`, after: `{"$id":"https://example.test/support","type":"integer"}`, want: "historical immutable URI"},
		{name: "parameterized schema", media: "application/schema+json; charset=utf-8", before: `{"$id":"https://example.test/support","type":"string"}`, after: `{"$id":"https://example.test/support","type":"integer"}`, want: "historical immutable URI"},
		{name: "generic schema container", media: "application/json", pointer: "#/schema", before: `{"schema":{"type":"string"}}`, after: `{"schema":{"type":"integer"}}`, want: "historical immutable URI"},
		{name: "schema without historical bytes", media: "application/json", before: `{"type":"string"}`, after: `{"type":"integer"}`, evidence: "missing", want: "historical document bytes"},
		{name: "schema with corrupt historical bytes", media: "application/json", before: `{"type":"string"}`, after: `{"type":"integer"}`, evidence: "corrupt", want: "historical artifact bytes"},
		{name: "schema with mismatched historical metadata", media: "application/json", before: `{"type":"string"}`, after: `{"type":"integer"}`, evidence: "metadata", want: "historical artifact metadata"},
		{name: "unchanged generic schema without historical bytes", media: "application/json", before: `{"type":"string"}`, after: `{"type":"string"}`, evidence: "missing"},
		{name: "generic context", context: true, media: "application/json", before: `{"@context":{"name":"https://vocab.example/name"}}`, after: `{"@context":{"name":"https://vocab.example/label"}}`},
		{name: "typed context", context: true, media: artifact.ContextMediaType, before: `{"@context":{"name":"https://vocab.example/name"}}`, after: `{"@context":{"name":"https://vocab.example/label"}}`},
		{name: "context payload", context: true, payload: true, before: `{"@context":{"name":"https://vocab.example/name"}}`, after: `{"@context":{"name":"https://vocab.example/label"}}`},
		{name: "context without historical bytes", context: true, media: "application/json", before: `{"@context":{}}`, after: `{"@context":{"name":"https://vocab.example/name"}}`, evidence: "missing", want: "historical document bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, c := populatedRelease(t)
			changeBoundSchema(&before, &c, func(a *artifact.Artifact) { a.Dependencies = nil })
			before.SchemaContextBindings = nil
			index, reference := 1, `{"$ref":"https://example.test/support`+test.pointer+`"}`
			if test.context {
				index, reference = 0, `{"@context":"https://example.test/support"}`
			}
			resource := "support.json"
			if test.payload {
				resource = ""
				setArtifactPayload(&before, c, index, test.before)
			} else {
				setArtifactPayload(&before, c, index, reference)
				addDocumentResource(&before, c, index, test.media, test.before)
			}
			selection := before.Artifacts[index]
			key := artifact.Dependency{Kind: selection.Kind, Type: selection.Type, Revision: selection.Revision}
			addCatalogEntry(&before, "https://example.test/support", selection.Revision, resource)
			require.NoError(t, before.Validate(c))
			after := before
			after.Artifacts = slices.Clone(before.Artifacts)
			after.URICatalog = slices.Clone(before.URICatalog)
			after.SchemaVersion = "2.0.0"
			c.History = []Release{{Manifest: before, Digest: digest(before.Raw)}}
			after.PreviousRelease = &PreviousRelease{SchemaVersion: before.SchemaVersion, ManifestDigest: c.History[0].Digest}
			bound := c.Artifacts[key]
			bound.Resources = slices.Clone(bound.Resources)
			bound.Revision += "-next"
			bound.CreatedUnderSchemaVersion = after.SchemaVersion
			bound.CanonicalURI += "-next"
			if test.payload {
				bound.PayloadBytes = []byte(test.after)
				bound.Payload = descriptor(bound.PayloadBytes, bound.Payload.MediaType, bound.Payload.BundlePath)
			} else {
				for i := range bound.Resources {
					if bound.Resources[i].Name == resource {
						bound.Resources[i].Bytes = []byte(test.after)
						bound.Resources[i].Descriptor = descriptor(bound.Resources[i].Bytes, test.media, bound.Resources[i].BundlePath)
					}
				}
			}
			after.Artifacts[index] = bound.Metadata()
			c.Artifacts[artifact.Dependency{Kind: bound.Kind, Type: bound.Type, Revision: bound.Revision}] = bound
			for i := range after.URICatalog {
				if after.URICatalog[i].ArtifactRevision == selection.Revision {
					after.URICatalog[i].ArtifactRevision = bound.Revision
				}
				if after.URICatalog[i].URI == selection.CanonicalURI {
					after.URICatalog[i].URI = bound.CanonicalURI
				}
			}
			old := c.Artifacts[key]
			switch test.evidence {
			case "missing":
				delete(c.Artifacts, key)
			case "corrupt":
				old.PayloadBytes = []byte("corrupt")
				c.Artifacts[key] = old
			case "metadata":
				old.Dependencies = []artifact.Dependency{{Kind: artifact.KindSchema, Type: "unknown", Revision: "unknown"}}
				c.Artifacts[key] = old
			}
			historyBefore := stringJSON(t, c.History)
			err := after.Validate(c)
			if test.want != "" {
				require.ErrorContains(t, err, test.want)
			} else {
				require.NoError(t, err)
			}
			var group sync.WaitGroup
			for range 4 {
				group.Go(func() {
					repeated := after.Validate(c)
					if err != nil {
						assert.EqualError(t, repeated, err.Error())
					} else {
						assert.NoError(t, repeated)
					}
				})
			}
			group.Wait()
			require.Equal(t, historyBefore, stringJSON(t, c.History))
		})
	}
}

// TestValidatePreservesCandidateAndHistoryConcurrently checks repeatable validation that preserves
// caller-owned candidate metadata and predecessor history.
func TestValidatePreservesCandidateAndHistoryConcurrently(t *testing.T) {
	m, c := populatedRelease(t)
	c.History = []Release{{Manifest: m, Digest: digest(m.Raw)}}
	m.SchemaVersion = "1.1.0"
	m.PreviousRelease = &PreviousRelease{SchemaVersion: "1.0.0", ManifestDigest: c.History[0].Digest}
	addCatalogEntry(&m, "https://example.test/added", "rev-1", "")
	historyBefore, err := json.Marshal(c.History)
	require.NoError(t, err)
	before, err := json.Marshal(m)
	require.NoError(t, err)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() { assert.NoError(t, m.Validate(c)) })
	}
	group.Wait()
	after, err := json.Marshal(m)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	historyAfter, err := json.Marshal(c.History)
	require.NoError(t, err)
	assert.Equal(t, historyBefore, historyAfter)
}

// TestValidateURIResourceChangeRoundTrip preserves same-revision resource updates through
// comparison, canonical parsing, and full validation.
func TestValidateURIResourceChangeRoundTrip(t *testing.T) {
	doc := populatedDocument(t)
	doc["previousRelease"] = nil
	delete(doc, "changes")
	schema := doc["artifacts"].([]any)[1].(map[string]any)
	resource := map[string]any{}
	for key, value := range schema["resources"].([]any)[0].(map[string]any) {
		resource[key] = value
	}
	resource["name"], resource["bundlePath"] = "other.txt", "resources/other.txt"
	schema["resources"] = append(schema["resources"].([]any), resource)
	before, err := Parse(canonicalDocument(t, doc))
	require.NoError(t, err)
	_, c := populatedRelease(t)
	key := artifact.Dependency{Kind: "schema", Type: "device", Revision: "rev-1"}
	bound := c.Artifacts[key]
	bound.Resources = append(bound.Resources, artifact.Resource{Name: "other.txt", Role: "metadata", Descriptor: descriptor([]byte("note"), "text/plain", "resources/other.txt"), Bytes: []byte("note")})
	c.Artifacts[key] = bound
	require.NoError(t, before.Validate(c))
	after := before
	after.SchemaVersion = "2.0.0"
	after.PreviousRelease = &PreviousRelease{SchemaVersion: before.SchemaVersion, ManifestDigest: digest(before.Raw)}
	after.URICatalog = append([]URIEntry(nil), before.URICatalog...)
	after.URICatalog[0].Resource = "other.txt"
	c.History = []Release{{Manifest: before, Digest: digest(before.Raw)}}
	changes, err := Diff(before, after, c)
	require.NoError(t, err)
	require.Equal(t, CompatibilityBreaking, changes.Compatibility)
	after.Changes = &changes
	require.NoError(t, after.Validate(c))
	doc["schemaVersion"] = after.SchemaVersion
	doc["previousRelease"] = map[string]any{"schemaVersion": before.SchemaVersion, "manifestDigest": digest(before.Raw)}
	doc["uriCatalog"].([]any)[0].(map[string]any)["resource"] = "other.txt"
	doc["changes"] = map[string]any{"compatibility": changes.Compatibility, "artifacts": changes.Artifacts, "schemaContextBindings": changes.SchemaContextBindings, "uriCatalog": changes.URICatalog, "storageCatalog": changes.StorageCatalog}
	data := canonicalDocument(t, doc)
	validated, err := Validate(data, c)
	require.NoError(t, err)
	require.Equal(t, data, validated.Raw)
}
