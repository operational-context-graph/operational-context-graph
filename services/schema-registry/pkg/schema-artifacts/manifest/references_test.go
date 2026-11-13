package manifest

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// TestDocumentReferenceCorpus checks offline resolution against shared valid and invalid document
// reference vectors.
func TestDocumentReferenceCorpus(t *testing.T) {
	var vectors struct {
		Format string
		Cases  []struct {
			ID, Kind, MediaType, ErrorContains string
			Payload, Support                   json.RawMessage
		}
	}
	require.NoError(t, json.Unmarshal(fixture(t, "document-references.json"), &vectors))
	require.Equal(t, "schema-registry-document-references-1", vectors.Format)
	for _, vector := range vectors.Cases {
		t.Run(vector.ID, func(t *testing.T) {
			m, c := populatedRelease(t)
			index := 1
			if vector.Kind == "context" {
				index = 0
			}
			setArtifactPayload(&m, c, index, string(vector.Payload))
			addDocumentResource(&m, c, index, vector.MediaType, string(vector.Support))
			addCatalogEntry(&m, "https://example.test/support", m.Artifacts[index].Revision, "support.json")
			err := m.Validate(c)
			if vector.ErrorContains != "" {
				require.ErrorContains(t, err, vector.ErrorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// setArtifactPayload updates bytes and integrity metadata in the selection and artifact context.
func setArtifactPayload(m *Manifest, c Context, index int, payload string) {
	selection := m.Artifacts[index]
	key := artifact.Dependency{Kind: selection.Kind, Type: selection.Type, Revision: selection.Revision}
	bound := c.Artifacts[key]
	bound.PayloadBytes = []byte(payload)
	bound.Payload = descriptor(bound.PayloadBytes, bound.Payload.MediaType, bound.Payload.BundlePath)
	c.Artifacts[key], m.Artifacts[index] = bound, bound.Metadata()
}

// addCatalogEntry adds a resolution entry and restores the manifest's UTF-16 catalog ordering.
func addCatalogEntry(m *Manifest, uri, revision, resource string) {
	m.URICatalog = append(m.URICatalog, URIEntry{URI: uri, ArtifactRevision: revision, Resource: resource})
	slices.SortFunc(m.URICatalog, func(a, b URIEntry) int { return compareUTF16(a.URI, b.URI) })
}

// addDocumentResource adds supporting JSON bytes while keeping selection and context descriptors
// consistent. It expects the populated-release fixture and retains existing note bytes when copying
// its resource metadata.
func addDocumentResource(m *Manifest, c Context, index int, media, payload string) {
	selection := m.Artifacts[index]
	key := artifact.Dependency{Kind: selection.Kind, Type: selection.Type, Revision: selection.Revision}
	bound := c.Artifacts[key]
	bound.Resources = append(bound.Metadata().Resources, artifact.Resource{Name: "support.json", Role: "support", Descriptor: descriptor([]byte(payload), media, "support/"+bound.Type+".json"), Bytes: []byte(payload)})
	// Retain the existing note resource bytes after copying selection metadata.
	for i := range bound.Resources {
		if bound.Resources[i].Name == "note.txt" {
			bound.Resources[i].Bytes = []byte("note")
		}
	}
	c.Artifacts[key], m.Artifacts[index] = bound, bound.Metadata()
}

// TestValidateNamedDocumentResources resolves schema and context references to owned resources and
// rejects later unresolved references.
func TestValidateNamedDocumentResources(t *testing.T) {
	for _, generic := range []bool{false, true} {
		m, c := populatedRelease(t)
		setArtifactPayload(&m, c, 1, `{"$ref":"https://example.test/support"}`)
		media := artifact.SchemaMediaType
		if generic {
			media = "application/json"
		}
		addDocumentResource(&m, c, 1, media, `{"type":"string"}`)
		addCatalogEntry(&m, "https://example.test/support", "rev-1", "support.json")
		require.NoError(t, m.Validate(c))
		setArtifactPayload(&m, c, 1, `{"allOf":[{"$ref":"https://example.test/support"},{"$ref":"https://unavailable.invalid/later"}]}`)
		require.ErrorContains(t, m.Validate(c), "no URI catalog entry")
	}
	m, c := populatedRelease(t)
	setArtifactPayload(&m, c, 0, `{"@context":[{"@vocab":"https://vocab.example/"},"https://example.test/context"]}`)
	addDocumentResource(&m, c, 0, artifact.ContextMediaType, `{"@context":{"name":"https://vocab.example/name"}}`)
	addCatalogEntry(&m, "https://example.test/context", "ctx-1", "support.json")
	require.NoError(t, m.Validate(c))
}

// TestValidateNestedIdentifierOwnership requires coverage and correct artifact ownership for relative
// nested schema identifiers.
func TestValidateNestedIdentifierOwnership(t *testing.T) {
	m, c := populatedRelease(t)
	setArtifactPayload(&m, c, 1, `{"$defs":{"child":{"$id":"child","type":"string"}}}`)
	uri := testOrigin + "/v1/resources/schema/device/child"
	require.ErrorContains(t, m.Validate(c), "missing required URI catalog entry")
	addCatalogEntry(&m, uri, "ctx-1", "")
	require.ErrorContains(t, m.Validate(c), "ownership mismatch")
	for i := range m.URICatalog {
		if m.URICatalog[i].URI == uri {
			m.URICatalog[i].ArtifactRevision = "rev-1"
		}
	}
	require.NoError(t, m.Validate(c))
	setArtifactPayload(&m, c, 1, `{"$defs":{"child":{"$id":"child","type":"string"}},"$ref":"child"}`)
	require.NoError(t, m.Validate(c))
}

// TestValidateDiscoveredDependencyAgreement requires exact dependencies for parsed cross-artifact
// references, including referenced extension containers.
// Internal references and literal URI strings do not create dependencies.
func TestValidateDiscoveredDependencyAgreement(t *testing.T) {
	m, c := populatedRelease(t)
	other := c.Artifacts[artifact.Dependency{Kind: "schema", Type: "device", Revision: "rev-1"}]
	other.Type, other.Revision = "location", "location-r1"
	other.PayloadBytes, other.Resources, other.Dependencies = []byte("true"), nil, nil
	other.Payload = descriptor(other.PayloadBytes, artifact.SchemaMediaType, "schemas/location.json")
	other.CanonicalURI = testOrigin + "/v1/resources/schema/location/location-r1"
	c.Artifacts[artifact.Dependency{Kind: other.Kind, Type: other.Type, Revision: other.Revision}] = other
	m.Artifacts = append(m.Artifacts, other.Metadata())
	m.StorageCatalog = append(m.StorageCatalog, StorageEntry{Kind: "schema", ArtifactType: "location", Stores: []Store{StoreDataCenterInventory}})
	addCatalogEntry(&m, other.CanonicalURI, other.Revision, "")
	setArtifactPayload(&m, c, 1, `{"$ref":"`+other.CanonicalURI+`"}`)
	require.ErrorContains(t, m.Validate(c), "lacks exact artifact dependency")
	changeBoundSchema(&m, &c, func(a *artifact.Artifact) {
		a.Dependencies = append(a.Dependencies, artifact.Dependency{Kind: other.Kind, Type: other.Type, Revision: other.Revision})
	})
	require.NoError(t, m.Validate(c))
	setArtifactPayload(&m, c, 1, `{"x-schema":{"$ref":"`+other.CanonicalURI+`"},"$ref":"#/x-schema"}`)
	require.NoError(t, m.Validate(c))
	changeBoundSchema(&m, &c, func(a *artifact.Artifact) { a.Dependencies = a.Dependencies[:1] })
	require.ErrorContains(t, m.Validate(c), "lacks exact artifact dependency")
	setArtifactPayload(&m, c, 1, `{"$defs":{"child":{"type":"string"}},"$ref":"#/$defs/child"}`)
	require.NoError(t, m.Validate(c))
	setArtifactPayload(&m, c, 1, `{"const":{"$ref":"https://unavailable.invalid/literal"}}`)
	require.NoError(t, m.Validate(c))
}

// TestValidateContextArraysAndNestedReferences rejects unavailable documents in context arrays,
// term-scoped contexts, and context imports.
func TestValidateContextArraysAndNestedReferences(t *testing.T) {
	for _, payload := range []string{
		`{"@context":[{},"https://unavailable.invalid/context"]}`,
		`{"@context":{"term":{"@id":"https://vocab.example/term","@context":"https://unavailable.invalid/context"}}}`,
		`{"@context":{"@import":"https://unavailable.invalid/context"}}`,
	} {
		m, c := populatedRelease(t)
		setArtifactPayload(&m, c, 0, payload)
		require.ErrorContains(t, m.Validate(c), "no URI catalog entry")
	}
}

// TestValidateEmbeddedResourcePointer resolves JSON Pointers against embedded resources using their
// own identifier scope.
func TestValidateEmbeddedResourcePointer(t *testing.T) {
	m, c := populatedRelease(t)
	setArtifactPayload(&m, c, 1, `{"$defs":{"child":{"$id":"nested/child","$defs":{"target":{"type":"string"}},"$ref":"#/$defs/target"}},"$ref":"nested/child"}`)
	addCatalogEntry(&m, testOrigin+"/v1/resources/schema/device/nested/child", "rev-1", "")
	require.NoError(t, m.Validate(c))
}

// TestValidateContextIRIKeysRemainExact preserves requested context IRI keys and rejects conflicting
// encoded aliases.
func TestValidateContextIRIKeysRemainExact(t *testing.T) {
	m, c := populatedRelease(t)
	const iri = "https://example.test/contexte-é"
	setArtifactPayload(&m, c, 0, `{"@context":"`+iri+`"}`)
	addDocumentResource(&m, c, 0, artifact.ContextMediaType, `{"@context":{}}`)
	addCatalogEntry(&m, iri, "ctx-1", "support.json")
	require.NoError(t, m.Validate(c))
	entry, exists := findURIEntry(m.URICatalog, iri)
	require.True(t, exists)
	require.Equal(t, iri, entry.URI)
	addCatalogEntry(&m, "https://example.test/contexte-%C3%A9", "rev-1", "")
	require.ErrorContains(t, m.Validate(c), "encoding aliases disagree")
}

// TestValidateIRIByteBoundary checks exact and overflowing IRI byte limits before URI encoding
// expands the representation.
func TestValidateIRIByteBoundary(t *testing.T) {
	const prefix = "https://example.test/"
	for _, size := range []int{schemaartifacts.MaxURIBytes, schemaartifacts.MaxURIBytes + 1} {
		m, c := populatedRelease(t)
		remaining := size - len(prefix)
		iri := prefix + strings.Repeat("é", remaining/2) + strings.Repeat("a", remaining%2)
		require.Len(t, iri, size)
		setArtifactPayload(&m, c, 0, `{"@context":"`+iri+`"}`)
		addDocumentResource(&m, c, 0, artifact.ContextMediaType, `{"@context":{}}`)
		addCatalogEntry(&m, iri, "ctx-1", "support.json")
		err := m.Validate(c)
		if size == schemaartifacts.MaxURIBytes {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}

// TestValidateIRIReferencesRequireDeclaredKeys rejects encoded aliases lacking the explicitly
// expressed reference or schema identifier key.
func TestValidateIRIReferencesRequireDeclaredKeys(t *testing.T) {
	m, c := populatedRelease(t)
	setArtifactPayload(&m, c, 0, `{"@context":[{},"https://example.test/contexte-é"]}`)
	addDocumentResource(&m, c, 0, artifact.ContextMediaType, `{"@context":{}}`)
	addCatalogEntry(&m, "https://example.test/contexte-%C3%A9", "ctx-1", "support.json")
	require.ErrorContains(t, m.Validate(c), "no URI catalog entry")
	m, c = populatedRelease(t)
	setArtifactPayload(&m, c, 1, `{"$defs":{"child":{"$id":"https://example.test/schéma","type":"string"}}}`)
	addCatalogEntry(&m, "https://example.test/sch%C3%A9ma", "rev-1", "")
	require.ErrorContains(t, m.Validate(c), "missing required URI catalog entry")
}
