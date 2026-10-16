// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
)

const testOrigin = "https://registry.example.test"

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
	assert.Equal(t, "breaking", diff.Compatibility)
	assert.True(t, validIncrement("1.0.0", after.SchemaVersion, diff.Compatibility))

	after.Artifacts[0].Dependencies = []artifact.Dependency{{Kind: artifact.KindContext, Type: "x", Revision: "y"}}
	diff, err = Diff(before, after, context)
	require.NoError(t, err)
	assert.Equal(t, "breaking", diff.Compatibility)
}

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

func TestParseRejectsCollectionLimit(t *testing.T) {
	items := strings.TrimRight(strings.Repeat(`null,`, MaxCollectionEntries+1), ",")
	data := []byte(`{"$schema":"https://example.test/manifest-1.0.0","artifacts":[` + items + `],"formatVersion":"1.0.0","previousRelease":null,"schemaContextBindings":[],"schemaVersion":"1.0.0","source":{"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":"https://git.example.test/repo"},"storageCatalog":[],"uriCatalog":[]}`)
	canonical, err := jcs.Transform(data)
	require.NoError(t, err)
	_, err = Parse(canonical)
	require.Error(t, err)
}

func TestParseRequiresExactJCSBytes(t *testing.T) {
	data := fixture(t, "valid-initial.json")
	canonical, err := jcs.Transform(data)
	require.NoError(t, err)
	require.Equal(t, data, canonical)
	_, err = Parse([]byte(` {"$schema":"https://example.test/manifest-1.0.0"}`))
	assert.Error(t, err)
}

func FuzzParse(f *testing.F) {
	f.Add(fixtureForFuzz("valid-initial.json"))
	f.Add([]byte(`{"$schema":"\xff"}`))
	f.Add([]byte(`{"$schema":"x","$schema":"x"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}

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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data := fixtureRaw(t, name)
	return []byte(strings.TrimSuffix(string(data), "\n"))
}

func fixtureRaw(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}

func fixtureForFuzz(name string) []byte {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return []byte(strings.TrimSuffix(string(data), "\n"))
}

func contextFor(manifest Manifest) Context {
	payload := []byte("true")
	item := artifact.Artifact{
		Kind: artifact.KindSchema, Type: "device", Revision: "rev-1", CreatedUnder: "1.0.0", SchemaClass: artifact.SchemaClassEntity,
		CanonicalURI: testOrigin + "/v1/resources/schema/device/rev-1", Payload: descriptor(payload, artifact.SchemaMediaType, "schemas/device.json"), PayloadBytes: payload,
	}
	return Context{RegistryOrigin: testOrigin, Artifacts: map[Key]artifact.Artifact{{Kind: item.Kind, Type: item.Type, Revision: item.Revision}: item}}
}

func descriptor(data []byte, mediaType, path string) artifact.Descriptor {
	digest := sha256.Sum256(data)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(digest[:]), Size: int64(len(data)), BundlePath: path}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
