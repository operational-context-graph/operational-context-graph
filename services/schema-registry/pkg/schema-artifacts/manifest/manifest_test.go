// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// testOrigin supplies the Registry origin shared by manifest conformance fixtures and their byte closures.
const testOrigin = "https://registry.example.test"

// TestParseAndValidateConformanceFixture checks parsed selection agreement, complete closure validation, and authoritative byte preservation.
func TestParseAndValidateConformanceFixture(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	parsed, err := Parse(data)
	require.NoError(t, err)
	bound := contextFor(parsed).Artifacts[Key{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}]
	assert.Equal(t, parsed.Artifacts[0].Kind, bound.Kind)
	assert.Equal(t, parsed.Artifacts[0].Type, bound.Type)
	assert.Equal(t, parsed.Artifacts[0].Revision, bound.Revision)
	assert.Equal(t, parsed.Artifacts[0].CreatedUnderSchemaVersion, bound.CreatedUnder)
	assert.Equal(t, parsed.Artifacts[0].SchemaClass, bound.SchemaClass)
	assert.Equal(t, parsed.Artifacts[0].CanonicalURI, bound.CanonicalURI)
	assert.Equal(t, parsed.Artifacts[0].Payload, bound.Payload)
	assert.Equal(t, len(parsed.Artifacts[0].Resources), len(bound.Resources))
	assert.Equal(t, len(parsed.Artifacts[0].Dependencies), len(bound.Dependencies))
	validated, err := Validate(data, contextFor(parsed))
	require.NoError(t, err)
	assert.Equal(t, parsed, validated)
	assert.Equal(t, data, validated.Raw)
}

// TestParseRejectsConformanceFailures checks unknown-field and trailing-newline rejection using repository fixtures.
func TestParseRejectsConformanceFailures(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "structural unknown field", file: "invalid-structural-unknown-field.json"},
		{name: "canonical trailing newline", file: "invalid-canonical-trailing-newline.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(fixtureRaw(t, test.file))
			assert.Error(t, err)
		})
	}
}

// TestParseDocumentURIBoundaries checks every declared document URI using UTF-8 byte boundaries.
func TestParseDocumentURIBoundaries(t *testing.T) {
	fields := []struct {
		name string
		set  func(map[string]any, string)
	}{
		{"schema", func(value map[string]any, uri string) { value["$schema"] = uri }},
		{"repository", func(value map[string]any, uri string) { value["source"].(map[string]any)["repository"] = uri }},
		{"catalog", func(value map[string]any, uri string) {
			value["uriCatalog"].([]any)[0].(map[string]any)["uri"] = uri
		}},
		{"canonical", func(value map[string]any, uri string) {
			value["artifacts"].([]any)[0].(map[string]any)["canonicalUri"] = uri
		}},
		{"change", func(value map[string]any, uri string) {
			value["changes"] = map[string]any{
				"summary": "URI addition", "compatibility": "additive", "artifacts": []any{},
				"schemaContextBindings": []any{}, "storageCatalog": []any{},
				"uriCatalog": []any{map[string]any{
					"operation": "add", "uri": uri, "afterRevision": "rev-1", "impact": "additive",
				}},
			}
		}},
	}
	for _, field := range fields {
		for _, unit := range []string{"a", "é"} {
			for _, size := range []int{schemaartifacts.MaxURIBytes - 1, schemaartifacts.MaxURIBytes, schemaartifacts.MaxURIBytes + 1} {
				t.Run(fmt.Sprintf("%s/%s/%d", field.name, unit, size), func(t *testing.T) {
					const prefix = "https://example.test/"
					remaining := size - len(prefix)
					uri := prefix + strings.Repeat(unit, remaining/len(unit)) + strings.Repeat("a", remaining%len(unit))
					require.Len(t, uri, size)
					var value map[string]any
					require.NoError(t, json.Unmarshal(fixture(t, "valid-initial.json"), &value))
					field.set(value, uri)
					encoded, err := json.Marshal(value)
					require.NoError(t, err)
					canonical, err := jcs.Transform(encoded)
					require.NoError(t, err)
					_, err = Parse(canonical)
					if size > schemaartifacts.MaxURIBytes {
						require.ErrorIs(t, err, ErrInvalid)
					} else {
						require.NoError(t, err)
					}
				})
			}
		}
	}
}

// TestConformanceCorpusNamesStableValidationOutcomes checks each fixture's declared classification, diagnostic, and canonical-byte preservation.
func TestConformanceCorpusNamesStableValidationOutcomes(t *testing.T) {
	data := fixtureRaw(t, "corpus.json")
	var corpus struct {
		Format   string `json:"format"`
		Fixtures []struct {
			File, Phase, Classification, ErrorContains string
			Raw                                        bool
		}
	}
	require.NoError(t, json.Unmarshal(data, &corpus))
	require.Equal(t, "schema-registry-conformance-corpus-1", corpus.Format)
	require.NotEmpty(t, corpus.Fixtures)
	classifications := map[string]bool{
		"valid":                           true,
		"format-duplicate-member":         false,
		"format-malformed-json":           false,
		"format-unknown-field":            false,
		"canonical-bom":                   false,
		"canonical-member-order":          false,
		"canonical-trailing-newline":      false,
		"serviceability-invalid-digest":   false,
		"serviceability-invalid-path":     false,
		"serviceability-invalid-selector": false,
		"serviceability-invalid-store":    false,
		"serviceability-invalid-version":  false,
		"serviceability-missing-required": false,
	}
	for _, entry := range corpus.Fixtures {
		t.Run(entry.File, func(t *testing.T) {
			require.Equal(t, "phase-2", entry.Phase)
			valid, known := classifications[entry.Classification]
			require.True(t, known, "unsupported classification %q", entry.Classification)
			data := fixture(t, entry.File)
			if entry.Raw {
				data = fixtureRaw(t, entry.File)
			}
			parsed, err := Parse(data)
			if valid {
				require.NoError(t, err)
				canonical, canonicalErr := jcs.Transform(data)
				require.NoError(t, canonicalErr)
				require.Equal(t, data, canonical)
				require.Equal(t, data, parsed.Raw)
				return
			}
			require.Error(t, err)
			require.NotEmpty(t, entry.ErrorContains)
			assert.ErrorContains(t, err, entry.ErrorContains)
		})
	}
}

// TestValidateRejectsClosureAndImmutableReuseFailures checks missing catalog entries, changed revision metadata, creation releases, and unresolved targets.
func TestValidateRejectsClosureAndImmutableReuseFailures(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	current, err := Parse(data)
	require.NoError(t, err)
	context := contextFor(current)

	withoutURI := current
	withoutURI.URICatalog = nil
	assert.Error(t, withoutURI.Validate(context))

	changed := current
	changed.Artifacts = append([]Artifact(nil), current.Artifacts...)
	changed.Artifacts[0].Extensions = map[string]any{"org.example.review": "changed"}
	context.History = []Release{{Manifest: current, Digest: digest(data)}}
	changed.PreviousRelease = &PreviousRelease{SchemaVersion: current.SchemaVersion, ManifestDigest: digest(data)}
	assert.Error(t, changed.Validate(context))

	wrongCreatedUnder := current
	wrongCreatedUnder.Artifacts = append([]Artifact(nil), current.Artifacts...)
	wrongCreatedUnder.Artifacts[0].CreatedUnderSchemaVersion = "0.9.0"
	assert.Error(t, wrongCreatedUnder.Validate(contextFor(current)))

	wrongCatalogTarget := current
	wrongCatalogTarget.URICatalog = []URIEntry{{URI: current.Artifacts[0].CanonicalURI, ArtifactRevision: "other-revision"}}
	assert.Error(t, wrongCatalogTarget.Validate(contextFor(current)))
}

// TestRequiredURIsRetainsOversizedIdentifiers prevents length rejection from silently bypassing catalog coverage checks.
func TestRequiredURIsRetainsOversizedIdentifiers(t *testing.T) {
	uri := "https://example.test/" + strings.Repeat("a", schemaartifacts.MaxURIBytes)
	for _, field := range []string{"$id", "@context"} {
		t.Run(field, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{field: uri})
			require.NoError(t, err)
			item := artifact.Artifact{CanonicalURI: testOrigin + "/v1/resources/schema/device/rev-1", PayloadBytes: payload}
			require.Contains(t, requiredURIs(item), uri)
		})
	}
}

// TestValidateURIChangeByteBoundaries checks typed successors removing aliases from previously accepted release history.
func TestValidateURIChangeByteBoundaries(t *testing.T) {
	for _, unit := range []string{"a", "é"} {
		for _, size := range []int{schemaartifacts.MaxURIBytes - 1, schemaartifacts.MaxURIBytes, schemaartifacts.MaxURIBytes + 1} {
			t.Run(fmt.Sprintf("%s/%d", unit, size), func(t *testing.T) {
				const prefix = "https://example.test/"
				remaining := size - len(prefix)
				uri := prefix + strings.Repeat(unit, remaining/len(unit)) + strings.Repeat("a", remaining%len(unit))
				before, err := Parse(fixture(t, "valid-initial.json"))
				require.NoError(t, err)
				after := before
				// Legacy history can contain aliases accepted before the URI length check existed.
				before.URICatalog = append([]URIEntry{{URI: uri, ArtifactRevision: "rev-1"}}, before.URICatalog...)
				context := contextFor(before)
				context.History = []Release{{Manifest: before, Digest: digest(before.Raw)}}
				after.SchemaVersion = "2.0.0"
				after.PreviousRelease = &PreviousRelease{SchemaVersion: before.SchemaVersion, ManifestDigest: digest(before.Raw)}
				changes, err := Diff(before, after, context)
				require.NoError(t, err)
				require.Len(t, changes.URICatalog, 1)
				after.Changes = &changes
				err = after.Validate(context)
				if size > schemaartifacts.MaxURIBytes {
					require.ErrorIs(t, err, ErrInvalid)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

// TestValidateRejectsUnorderedUTF16Arrays distinguishes canonical code-unit ordering from Unicode code-point ordering.
func TestValidateRejectsUnorderedUTF16Arrays(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	current, err := Parse(data)
	require.NoError(t, err)
	current.Artifacts = append(current.Artifacts, current.Artifacts[0])
	current.Artifacts[1].Type = "\U00010000"
	current.Artifacts[1].Revision = "rev-2"
	current.Artifacts[1].CanonicalURI = testOrigin + "/v1/resources/schema/x/rev-2"
	current.Artifacts[0].Type = "\ue000"
	current.Artifacts[0].Revision = "rev-3"
	current.Artifacts[0].CanonicalURI = testOrigin + "/v1/resources/schema/y/rev-3"
	assert.ErrorContains(t, current.validateOrder(), "artifacts are not ordered")
	assert.Less(t, compareUTF16("\U00010000", "\ue000"), 0)
}

// TestDiffClassifiesChangesAndRequiredVersionIncrement checks that URI reassignment remains breaking despite an annotation-only schema update.
func TestDiffClassifiesChangesAndRequiredVersionIncrement(t *testing.T) {
	beforeData := fixture(t, "valid-initial.json")
	before, err := Parse(beforeData)
	require.NoError(t, err)
	after := before
	after.SchemaVersion = "2.0.0"
	after.Artifacts = append([]Artifact(nil), before.Artifacts...)
	after.Artifacts[0].Revision = "rev-2"
	after.Artifacts[0].CreatedUnderSchemaVersion = "2.0.0"
	after.URICatalog = []URIEntry{{URI: before.Artifacts[0].CanonicalURI, ArtifactRevision: "rev-2"}}
	context := contextFor(before)
	oldBound := context.Artifacts[Key{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}]
	oldBound.PayloadBytes = []byte(`{"type":"object"}`)
	oldBound.Payload = descriptor(oldBound.PayloadBytes, artifact.SchemaMediaType, "schemas/device.json")
	context.Artifacts[Key{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}] = oldBound
	newBound := context.Artifacts[Key{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}]
	newBound.Revision = "rev-2"
	newBound.CreatedUnder = "1.0.1"
	newBound.CanonicalURI = before.Artifacts[0].CanonicalURI
	newBound.PayloadBytes = []byte(`{"description":"new","type":"object"}`)
	newBound.Payload = descriptor(newBound.PayloadBytes, artifact.SchemaMediaType, "schemas/device-2.json")
	context.Artifacts[Key{Kind: artifact.KindSchema, Type: "device", Revision: "rev-2"}] = newBound
	assert.True(t, sameSchemaRelations(before.Artifacts[0], after.Artifacts[0]))
	assert.True(t, annotationOnly(before.Artifacts[0], after.Artifacts[0], context))
	diff, err := Diff(before, after, context)
	require.NoError(t, err)
	assert.Empty(t, diff.SchemaContextBindings)
	assert.Equal(t, CompatibilityBreaking, diff.Compatibility)
	assert.True(t, validIncrement("1.0.0", after.SchemaVersion, diff.Compatibility))

	after.Artifacts[0].Dependencies = []artifact.Dependency{{Kind: artifact.KindContext, Type: "x", Revision: "y"}}
	diff, err = Diff(before, after, context)
	require.NoError(t, err)
	assert.Equal(t, CompatibilityBreaking, diff.Compatibility)
}

// TestDiffClassifiesVersionOneNetChangeCases checks compatibility precedence across artifact, binding, URI, and storage differences.
func TestDiffClassifiesVersionOneNetChangeCases(t *testing.T) {
	base := Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}}, URICatalog: []URIEntry{{URI: "https://example.test/device", ArtifactRevision: "rev-1"}}}
	tests := []struct {
		name  string
		want  Compatibility
		after Manifest
	}{
		{"add", "additive", Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}, {Kind: artifact.KindSchema, Type: "location", Revision: "rev-2"}}, URICatalog: base.URICatalog}},
		{"remove", "breaking", Manifest{Artifacts: nil, URICatalog: nil}},
		{"schema-update-unknown", "breaking", Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-2"}}, URICatalog: []URIEntry{{URI: "https://example.test/device", ArtifactRevision: "rev-2"}}}},
		{"uri-update", "breaking", Manifest{Artifacts: base.Artifacts, URICatalog: []URIEntry{{URI: "https://example.test/device", ArtifactRevision: "rev-2"}}}},
		{"binding-remove", "breaking", Manifest{Artifacts: base.Artifacts, URICatalog: base.URICatalog}},
		{"storage-add", "additive", Manifest{Artifacts: base.Artifacts, URICatalog: base.URICatalog, StorageCatalog: []StorageEntry{{Kind: artifact.KindSchema, ArtifactType: "device", Stores: []Store{StoreTelemetryStore}}}}},
	}
	base.SchemaContextBindings = []Binding{{SchemaRevision: "rev-1", ContextRevisions: []string{"ctx-1"}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name != "binding-remove" {
				test.after.SchemaContextBindings = base.SchemaContextBindings
			}
			diff, err := Diff(base, test.after, Context{Artifacts: map[Key]artifact.Artifact{}})
			require.NoError(t, err)
			assert.Equal(t, test.want, diff.Compatibility)
		})
	}
}

// TestDiffClassifiesContextAndSourceMappingUpdatesDirectly keeps non-schema revision updates conservatively breaking.
func TestDiffClassifiesContextAndSourceMappingUpdatesDirectly(t *testing.T) {
	t.Parallel()
	baseArtifacts := []Artifact{
		{Kind: artifact.KindSchema, Type: "device", Revision: "schema-1"},
		{Kind: artifact.KindContext, Type: "device-context", Revision: "context-1"},
		{Kind: artifact.KindSourceMapping, Type: "device-map", Revision: "mapping-1"},
	}
	tests := []struct {
		name, kind, artifactType, beforeRevision, afterRevision string
	}{
		{"context", string(artifact.KindContext), "device-context", "context-1", "context-2"},
		{"source mapping", string(artifact.KindSourceMapping), "device-map", "mapping-1", "mapping-2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := Manifest{Artifacts: append([]Artifact(nil), baseArtifacts...)}
			after := Manifest{Artifacts: append([]Artifact(nil), baseArtifacts...)}
			for index := range after.Artifacts {
				if string(after.Artifacts[index].Kind) == test.kind {
					after.Artifacts[index].Revision = test.afterRevision
				}
			}
			diff, err := Diff(before, after, Context{Artifacts: map[Key]artifact.Artifact{}})
			require.NoError(t, err)
			require.Len(t, diff.Artifacts, 1)
			assert.Equal(t, OperationUpdate, diff.Artifacts[0].Operation)
			assert.Equal(t, test.artifactType, diff.Artifacts[0].ArtifactType)
			assert.Equal(t, test.beforeRevision, diff.Artifacts[0].BeforeRevision)
			assert.Equal(t, test.afterRevision, diff.Artifacts[0].AfterRevision)
			assert.Equal(t, ImpactBreaking, diff.Artifacts[0].Impact)
			assert.Equal(t, CompatibilityBreaking, diff.Compatibility)
		})
	}
}

// TestDiffUsesNetEndpointsAndDeterministicOrdering excludes intermediate changes and orders logical artifact differences consistently.
func TestDiffUsesNetEndpointsAndDeterministicOrdering(t *testing.T) {
	before := Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}, {Kind: artifact.KindSchema, Type: "location", Revision: "rev-2"}}}
	after := Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "asset", Revision: "rev-3"}, {Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}}}
	diff, err := Diff(before, after, Context{Artifacts: map[Key]artifact.Artifact{}})
	require.NoError(t, err)
	// The result compares endpoints only. It does not include an intermediate
	// addition that was removed before the target manifest.
	require.Len(t, diff.Artifacts, 2)
	assert.Equal(t, OperationAdd, diff.Artifacts[0].Operation)
	assert.Equal(t, "asset", diff.Artifacts[0].ArtifactType)
	assert.Equal(t, OperationRemove, diff.Artifacts[1].Operation)
	assert.Equal(t, "location", diff.Artifacts[1].ArtifactType)
	assert.Equal(t, CompatibilityBreaking, diff.Compatibility)
}

// TestDiffClassifiesAnnotationOnlySchemaUpdateAsOperational checks the narrow operational exception when schema relations remain unchanged.
func TestDiffClassifiesAnnotationOnlySchemaUpdateAsOperational(t *testing.T) {
	before := Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}}}
	after := Manifest{Artifacts: []Artifact{{Kind: artifact.KindSchema, Type: "device", Revision: "rev-2"}}}
	context := Context{Artifacts: map[Key]artifact.Artifact{
		{Kind: artifact.KindSchema, Type: "device", Revision: "rev-1"}: {Kind: artifact.KindSchema, Type: "device", Revision: "rev-1", Payload: artifact.Descriptor{MediaType: artifact.SchemaMediaType}, PayloadBytes: []byte(`{"type":"object","description":"old"}`)},
		{Kind: artifact.KindSchema, Type: "device", Revision: "rev-2"}: {Kind: artifact.KindSchema, Type: "device", Revision: "rev-2", Payload: artifact.Descriptor{MediaType: artifact.SchemaMediaType}, PayloadBytes: []byte(`{"type":"object","description":"new"}`)},
	}}
	diff, err := Diff(before, after, context)
	require.NoError(t, err)
	require.Len(t, diff.Artifacts, 1)
	assert.Equal(t, ImpactOperational, diff.Artifacts[0].Impact)
	assert.Equal(t, CompatibilityOperational, diff.Compatibility)
}

// TestDiffOrdersEveryChangeCollection verifies canonical artifact, binding, URI, and storage change sort keys.
func TestDiffOrdersEveryChangeCollection(t *testing.T) {
	before := Manifest{
		Artifacts: []Artifact{
			{Kind: artifact.KindSchema, Type: "zebra", Revision: "rev-z-1"},
			{Kind: artifact.KindSchema, Type: "alpha", Revision: "rev-a-1"},
		},
		SchemaContextBindings: []Binding{
			{SchemaRevision: "schema-z", ContextRevisions: []string{"context-z", "context-a"}},
			{SchemaRevision: "schema-a", ContextRevisions: []string{"context-b"}},
		},
		URICatalog:     []URIEntry{{URI: "https://example.test/z", ArtifactRevision: "rev-z-1"}, {URI: "https://example.test/a", ArtifactRevision: "rev-a-1"}},
		StorageCatalog: []StorageEntry{{Kind: artifact.KindSchema, ArtifactType: "zebra", Stores: []Store{StoreTelemetryStore, StoreDataCenterInventory}}},
	}
	after := Manifest{
		Artifacts: []Artifact{
			{Kind: artifact.KindSchema, Type: "alpha", Revision: "rev-a-2"},
			{Kind: artifact.KindSchema, Type: "middle", Revision: "rev-m-1"},
		},
		SchemaContextBindings: []Binding{{SchemaRevision: "schema-c", ContextRevisions: []string{"context-c"}}},
		URICatalog:            []URIEntry{{URI: "https://example.test/a", ArtifactRevision: "rev-a-2"}, {URI: "https://example.test/m", ArtifactRevision: "rev-m-1"}},
		StorageCatalog:        []StorageEntry{{Kind: artifact.KindSchema, ArtifactType: "alpha", Stores: []Store{StoreDataCenterInventory}}, {Kind: artifact.KindSchema, ArtifactType: "zebra", Stores: []Store{StoreTelemetryStore}}},
	}

	diff, err := Diff(before, after, Context{Artifacts: map[Key]artifact.Artifact{}})
	require.NoError(t, err)
	require.Len(t, diff.Artifacts, 3)
	assert.Equal(t, []string{"alpha", "middle", "zebra"}, []string{diff.Artifacts[0].ArtifactType, diff.Artifacts[1].ArtifactType, diff.Artifacts[2].ArtifactType})
	require.Len(t, diff.SchemaContextBindings, 4)
	assert.Equal(t, []string{"schema-a", "schema-c", "schema-z", "schema-z"}, []string{diff.SchemaContextBindings[0].SchemaRevision, diff.SchemaContextBindings[1].SchemaRevision, diff.SchemaContextBindings[2].SchemaRevision, diff.SchemaContextBindings[3].SchemaRevision})
	assert.Equal(t, []string{"context-b", "context-c", "context-a", "context-z"}, []string{diff.SchemaContextBindings[0].ContextRevision, diff.SchemaContextBindings[1].ContextRevision, diff.SchemaContextBindings[2].ContextRevision, diff.SchemaContextBindings[3].ContextRevision})
	require.Len(t, diff.URICatalog, 3)
	assert.Equal(t, []string{"https://example.test/a", "https://example.test/m", "https://example.test/z"}, []string{diff.URICatalog[0].URI, diff.URICatalog[1].URI, diff.URICatalog[2].URI})
	require.Len(t, diff.StorageCatalog, 2)
	assert.Equal(t, []string{"alpha", "zebra"}, []string{diff.StorageCatalog[0].ArtifactType, diff.StorageCatalog[1].ArtifactType})
}

// TestValidateIsRaceSafe validates shared immutable fixture bytes and closure data from concurrent callers.
func TestValidateIsRaceSafe(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	parsed, err := Parse(data)
	require.NoError(t, err)
	context := contextFor(parsed)
	const workers = 32
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := Validate(data, context)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		assert.NoError(t, err)
	}
}

// TestParseRejectsCollectionLimit rejects a canonical manifest whose artifact array exceeds the entry limit.
func TestParseRejectsCollectionLimit(t *testing.T) {
	items := strings.TrimRight(strings.Repeat(`null,`, schemaartifacts.MaxArrayEntries+1), ",")
	data := []byte(`{"$schema":"https://example.test/manifest-1.0.0","artifacts":[` + items + `],"formatVersion":"1.0.0","previousRelease":null,"schemaContextBindings":[],"schemaVersion":"1.0.0","source":{"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":"https://git.example.test/repo"},"storageCatalog":[],"uriCatalog":[]}`)
	canonical, err := jcs.Transform(data)
	require.NoError(t, err)
	_, err = Parse(canonical)
	require.Error(t, err)
}

// TestParseRequiresExactJCSBytes checks exact canonical encoding rather than accepting equivalent JSON with extra whitespace.
func TestParseRequiresExactJCSBytes(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	canonical, err := jcs.Transform(data)
	require.NoError(t, err)
	require.Equal(t, data, canonical)
	_, err = Parse([]byte(` {"$schema":"https://example.test/manifest-1.0.0"}`))
	assert.Error(t, err)
}

// FuzzParse exercises manifest parsing with canonical fixtures, invalid encoding, duplicate members, and arbitrary mutations.
func FuzzParse(f *testing.F) {
	f.Add(fixtureForFuzz("valid-initial.json"))
	f.Add([]byte(`{"$schema":"\xff"}`))
	f.Add([]byte(`{"$schema":"x","$schema":"x"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}

// BenchmarkParseCanonicalManifest measures allocations and throughput for the initial-release conformance manifest.
func BenchmarkParseCanonicalManifest(b *testing.B) {
	data := fixtureForFuzz("valid-initial.json")
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		_, err := Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// fixture loads a repository fixture and removes one optional final newline before canonical parsing.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data := fixtureRaw(t, name)
	return []byte(strings.TrimSuffix(string(data), "\n"))
}

// fixtureRaw loads exact repository fixture bytes, preserving intentionally invalid encodings and trailing newlines.
func fixtureRaw(t *testing.T, name string) []byte {
	t.Helper()
	// #nosec G304 -- name selects a repository-controlled fixture.
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

// fixtureForFuzz loads newline-trimmed seed bytes and panics if a required repository fixture is unavailable.
func fixtureForFuzz(name string) []byte {
	// #nosec G304 -- name selects a repository-controlled fixture.
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return []byte(strings.TrimSuffix(string(data), "\n"))
}

// contextFor supplies the fixed device-schema byte closure used by initial-release fixtures.
func contextFor(manifest Manifest) Context {
	payload := []byte("true")
	item := artifact.Artifact{
		Kind: artifact.KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: artifact.SchemaClassEntity,
		CanonicalURI: testOrigin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, artifact.SchemaMediaType, "schemas/device.json"), PayloadBytes: payload,
	}
	return Context{RegistryOrigin: testOrigin, Artifacts: map[Key]artifact.Artifact{{Kind: item.Kind, Type: item.Type, Revision: item.Revision}: item}}
}

// descriptor builds byte-integrity metadata for a test payload at its declared bundle location.
func descriptor(data []byte, mediaType, path string) artifact.Descriptor {
	digest := sha256.Sum256(data)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: path}
}

// digest computes the Registry's exact SHA-256 spelling for fixture bytes.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
