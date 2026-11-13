package manifest

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// TestDiffSchemaCompatibilityCorpus checks impacts for annotations, literal data, and optional
// additions under concurrent comparison.
func TestDiffSchemaCompatibilityCorpus(t *testing.T) {
	var corpus struct {
		Format string
		Cases  []struct {
			ID, Rule      string
			Before, After json.RawMessage
			Impact        Impact
		}
	}
	require.NoError(t, json.Unmarshal(fixture(t, "schema-changes.json"), &corpus))
	require.Equal(t, "schema-registry-schema-changes-1", corpus.Format)
	for _, test := range corpus.Cases {
		t.Run(test.ID, func(t *testing.T) {
			require.NotEmpty(t, test.Rule)
			before := artifact.Artifact{Kind: "schema", Type: "device", Revision: "r1"}
			after := before
			after.Revision = "r2"
			c := Context{Artifacts: map[artifact.Dependency]artifact.Artifact{
				{Kind: "schema", Type: "device", Revision: "r1"}: {Payload: artifact.Descriptor{MediaType: artifact.SchemaMediaType}, PayloadBytes: test.Before},
				{Kind: "schema", Type: "device", Revision: "r2"}: {Payload: artifact.Descriptor{MediaType: artifact.SchemaMediaType}, PayloadBytes: test.After},
			}}
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					changes, err := Diff(Manifest{Artifacts: []artifact.Artifact{before}}, Manifest{Artifacts: []artifact.Artifact{after}}, c)
					assert.NoError(t, err)
					assert.Equal(t, test.Impact, changes.Artifacts[0].Impact)
				})
			}
			group.Wait()
		})
	}
}

// TestDiffMappingMetadataEquivalence distinguishes descriptor serialization from behavior-bearing
// changes without interpreting program bytes.
// Repeated concurrent comparisons MUST return the same impacts.
func TestDiffMappingMetadataEquivalence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*artifact.Artifact)
		want   Impact
	}{
		{"serialization", func(a *artifact.Artifact) { a.PayloadBytes = append([]byte(" \n"), a.PayloadBytes...) }, ImpactOperational},
		{"object member order", func(a *artifact.Artifact) {
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(a.PayloadBytes, &fields))
			data := []byte("{")
			for i, key := range []string{"programs", "engine", "sourceSchema", "id", "formatVersion", "$schema"} {
				if i > 0 {
					data = append(data, ',')
				}
				name, err := json.Marshal(key)
				require.NoError(t, err)
				data = append(data, name...)
				data = append(data, ':')
				data = append(data, fields[key]...)
			}
			a.PayloadBytes = append(data, '}')
		}, ImpactOperational},
		{"program digest", func(a *artifact.Artifact) { a.Resources[0].Digest = digest([]byte("changed")) }, ImpactBreaking},
		{"target version", func(a *artifact.Artifact) { a.TargetSchema = "2.0.0" }, ImpactBreaking},
		{"transformation order", func(a *artifact.Artifact) {
			var value map[string]any
			require.NoError(t, json.Unmarshal(a.PayloadBytes, &value))
			entries := value["programs"].(map[string]any)["transformations"].([]any)
			entries[0], entries[1] = entries[1], entries[0]
			var err error
			a.PayloadBytes, err = json.Marshal(value)
			require.NoError(t, err)
		}, ImpactBreaking},
		{"missing proof", func(a *artifact.Artifact) { a.PayloadBytes = nil }, ImpactBreaking},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, c := mappingClosure(t)
			before := m.Artifacts[3]
			bound := c.Artifacts[artifact.Dependency{Kind: before.Kind, Type: before.Type, Revision: before.Revision}]
			bound.Resources = append([]artifact.Resource(nil), bound.Resources...)
			after := bound.Metadata()
			after.Revision = "mapping-a-r2"
			bound.Revision = after.Revision
			test.mutate(&bound)
			bound.Payload = descriptor(bound.PayloadBytes, bound.Payload.MediaType, bound.Payload.BundlePath)
			after = bound.Metadata()
			c.Artifacts[artifact.Dependency{Kind: after.Kind, Type: after.Type, Revision: after.Revision}] = bound
			changes, err := Diff(Manifest{Artifacts: []artifact.Artifact{before}}, Manifest{Artifacts: []artifact.Artifact{after}}, c)
			require.NoError(t, err)
			require.Equal(t, test.want, changes.Artifacts[0].Impact)
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					repeated, err := Diff(Manifest{Artifacts: []artifact.Artifact{before}}, Manifest{Artifacts: []artifact.Artifact{after}}, c)
					assert.NoError(t, err)
					assert.Equal(t, changes, repeated)
				})
			}
			group.Wait()
		})
	}
}

// TestDiffAssociationImpacts distinguishes new artifact associations from changes to existing
// interpretation or destinations using available safety evidence.
func TestDiffAssociationImpacts(t *testing.T) {
	old := artifact.Artifact{Kind: "schema", Type: "device", Revision: "r1"}
	fresh := artifact.Artifact{Kind: "schema", Type: "location", Revision: "r2"}
	before := Manifest{Artifacts: []artifact.Artifact{old}}
	for _, introduced := range []bool{false, true} {
		after := Manifest{Artifacts: []artifact.Artifact{old}}
		target := old
		want := ImpactBreaking
		if introduced {
			target, want = fresh, ImpactAdditive
			after.Artifacts = append(after.Artifacts, fresh)
		}
		after.SchemaContextBindings = []Binding{{SchemaRevision: target.Revision, ContextRevisions: []string{"context-r1"}}}
		after.StorageCatalog = []StorageEntry{{Kind: "schema", ArtifactType: target.Type, Stores: []Store{StoreTelemetryStore}}}
		after.URICatalog = []URIEntry{{URI: "https://example.test/identifier", ArtifactRevision: target.Revision}}
		changes, err := Diff(before, after, Context{})
		require.NoError(t, err)
		assert.Equal(t, want, changes.SchemaContextBindings[0].Impact)
		assert.Equal(t, want, changes.StorageCatalog[0].Impact)
		// Target identity alone does not establish safety for a requested alias.
		assert.Equal(t, ImpactBreaking, changes.URICatalog[0].Impact)
		assert.Equal(t, CompatibilityBreaking, changes.Compatibility)
	}
	alias := Manifest{Artifacts: before.Artifacts, URICatalog: []URIEntry{{URI: "https://example.test/alias", ArtifactRevision: "r1"}}}
	c := Context{Artifacts: map[artifact.Dependency]artifact.Artifact{{Kind: "schema", Type: "device", Revision: "r1"}: {PayloadBytes: []byte("true")}}}
	changes, err := Diff(before, alias, c)
	require.NoError(t, err)
	assert.Equal(t, ImpactAdditive, changes.URICatalog[0].Impact)
	bound := c.Artifacts[artifact.Dependency{Kind: "schema", Type: "device", Revision: "r1"}]
	bound.PayloadBytes = []byte(`{"const":"https://example.test/alias"}`)
	c.Artifacts[artifact.Dependency{Kind: "schema", Type: "device", Revision: "r1"}] = bound
	changes, err = Diff(before, alias, c)
	require.NoError(t, err)
	assert.Equal(t, ImpactBreaking, changes.URICatalog[0].Impact)
	for _, target := range []artifact.Artifact{old, fresh} {
		for _, evidence := range []struct {
			name, payload string
			want          Impact
		}{
			{"unused", "true", ImpactAdditive},
			{"existing identifier", `{"const":"https://example.test/alias"}`, ImpactBreaking},
			{"missing bytes", "", ImpactBreaking},
		} {
			t.Run(target.Type+"/"+evidence.name, func(t *testing.T) {
				after := Manifest{Artifacts: []artifact.Artifact{old}, URICatalog: []URIEntry{{URI: "https://example.test/alias", ArtifactRevision: target.Revision}}}
				if target.Revision == fresh.Revision {
					after.Artifacts = append(after.Artifacts, fresh)
				}
				c := Context{Artifacts: map[artifact.Dependency]artifact.Artifact{
					{Kind: old.Kind, Type: old.Type, Revision: old.Revision}: {PayloadBytes: []byte(evidence.payload)},
				}}
				changes, err := Diff(before, after, c)
				require.NoError(t, err)
				require.Equal(t, evidence.want, changes.URICatalog[0].Impact)
			})
		}
	}
}

// TestURIAdditionProofPrefixes checks alias lookup against exact identifiers, nested vocabulary
// prefixes, and unrelated URIs.
func TestURIAdditionProofPrefixes(t *testing.T) {
	a := artifact.Artifact{Kind: "schema", Type: "device", Revision: "r1"}
	c := Context{Artifacts: map[artifact.Dependency]artifact.Artifact{
		{Kind: a.Kind, Type: a.Type, Revision: a.Revision}: {PayloadBytes: []byte(`{"const":["https://vocab.example/","https://vocab.example/deep/","https://other.example/zz"]}`)},
	}}
	proof := uriAdditionProof(Manifest{Artifacts: []artifact.Artifact{a}}, c)
	for _, test := range []struct {
		uri    string
		unused bool
	}{
		{"https://vocab.example/new", false},
		{"https://vocab.example/deep/next", false},
		{"https://other.example/zz", false},
		{"https://other.example/zz/next", false},
		{"https://other.example/aa", true},
		{"https://empty.example/new", true},
	} {
		require.Equal(t, test.unused, proof(test.uri), test.uri)
	}
}
