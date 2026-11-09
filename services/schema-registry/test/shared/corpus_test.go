// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCorpus(t *testing.T) {
	corpus := LoadCorpus(t)
	require.Equal(t, "schema-registry-release-corpus-1", corpus.Format)
	require.Equal(t, "https://registry.example.test", corpus.Origin)
	require.Equal(t, "https://example.test/manifest-1.0.0", corpus.Schema)
	require.Equal(t, "https://git.example.test/model", corpus.Source.Repository)
	require.Len(t, corpus.Source.Commit, 40)
	require.Len(t, corpus.Releases, 3)
	initial := corpus.Releases[0]
	require.Equal(t, "multi-candidate", initial.Name)
	require.Empty(t, initial.Parent)
	require.Equal(t, "initial", initial.Compatibility)
	require.Len(t, initial.Artifacts, 8)
	mapping := initial.Artifacts[5]
	require.Equal(t, "source-mapping", mapping.Kind)
	require.Equal(t, "mapping-a.json", mapping.PayloadFile)
	require.Equal(t, []byte{0, 255}, mapping.Resources[0].Content)
	require.Empty(t, mapping.Resources[2].Content)
	require.Equal(t, []string{"edge", "entity", "identifier", "source"}, mapping.Dependencies)
	require.Equal(t, []string{"context-r1"}, initial.Artifacts[1].Contexts)
	require.Equal(t, []string{"data-center-inventory", "telemetry-store"}, initial.Artifacts[1].Stores)
	require.Equal(t, "multi-candidate", corpus.Releases[1].Parent)
	require.Equal(t, "breaking", corpus.Releases[1].Compatibility)
}

func TestLoadFailures(t *testing.T) {
	cases := LoadFailures(t)
	require.Len(t, cases, 91)
	first := cases[0]
	require.Equal(t, "manifest-empty", first.ID)
	require.Equal(t, "manifest.top-level", first.Rule)
	require.Equal(t, "parse", first.Phase)
	require.Equal(t, "malformed-json", first.Classification)
	require.Equal(t, "empty input", first.ErrorContains)
	require.Equal(t, "raw", first.Target)
	require.Equal(t, "empty", first.Operation)
	for _, entry := range cases {
		if entry.ID == "profile-transformations" {
			require.Equal(t, float64(10001), entry.Value, "mutations retain encoding/json numeric representation")
			return
		}
	}
	t.Fatal("missing transformation boundary fixture")
}

func TestReadFixturePreservesBytesOutsidePackageDirectory(t *testing.T) {
	expected := map[string][]byte{}
	for _, name := range []string{"invalid-canonical-bom.json", "invalid-canonical-trailing-newline.json", "mapping-a.json"} {
		// #nosec G304 -- name selects a repository-controlled fixture.
		data, err := os.ReadFile(filepath.Join("..", "fixtures", "manifest", name))
		require.NoError(t, err)
		expected[name] = data
	}
	t.Chdir(t.TempDir())
	for name, data := range expected {
		t.Run(name, func(t *testing.T) {
			got := ReadFixture(t, name)
			require.Equal(t, data, got)
			got[0] ^= 0xff
			require.Equal(t, data, ReadFixture(t, name), "reads return independently owned bytes")
		})
	}
	require.Len(t, LoadCorpus(t).Releases, 3)
	require.NotEmpty(t, LoadFailures(t))
}

func TestReadFixtureRejectsInvalidPaths(t *testing.T) {
	for _, name := range []string{"", "../README.md", "../../go.mod", filepath.Join(t.TempDir(), "outside.json")} {
		t.Run(name, func(t *testing.T) {
			data, err := readFixture(name)
			require.ErrorContains(t, err, "fixture path must remain inside")
			require.Nil(t, data)
		})
	}
	data, err := readFixture("does-not-exist.json")
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Nil(t, data)
}

func TestReadJSONRejectsMissingAndMalformedData(t *testing.T) {
	var value any
	require.ErrorIs(t, readJSON("does-not-exist.json", &value), os.ErrNotExist)
	require.ErrorContains(t, readJSON("invalid-format-syntax.json", &value), "decode corpus invalid-format-syntax.json")
	var array []string
	require.ErrorContains(t, readJSON("mapping-a.json", &array), "decode corpus mapping-a.json")
}

func TestConcurrentLoadsReturnIndependentData(t *testing.T) {
	for range 8 {
		t.Run("caller", func(t *testing.T) {
			t.Parallel()
			corpus := LoadCorpus(t)
			corpus.Releases[0].Name = "changed"
			corpus.Releases[0].Artifacts[5].Resources[0].Content[0] = 42
			corpus.Releases[0].Artifacts[5].Dependencies[0] = "changed"
			corpus.Releases[0].Artifacts[1].Stores[0] = "changed"
			corpus.Releases[0].Artifacts[1].Contexts[0] = "changed"
			fresh := LoadCorpus(t)
			require.Equal(t, "multi-candidate", fresh.Releases[0].Name)
			require.Equal(t, []byte{0, 255}, fresh.Releases[0].Artifacts[5].Resources[0].Content)
			require.Equal(t, "edge", fresh.Releases[0].Artifacts[5].Dependencies[0])
			require.Equal(t, "data-center-inventory", fresh.Releases[0].Artifacts[1].Stores[0])
			require.Equal(t, "context-r1", fresh.Releases[0].Artifacts[1].Contexts[0])
			cases := LoadFailures(t)
			cases[0].ID = "changed"
			for _, entry := range cases {
				if object, ok := entry.Value.(map[string]any); ok {
					object["loader-test-only"] = true
				}
			}
			freshCases := LoadFailures(t)
			require.Equal(t, "manifest-empty", freshCases[0].ID)
			for _, entry := range freshCases {
				if object, ok := entry.Value.(map[string]any); ok {
					require.NotContains(t, object, "loader-test-only")
				}
			}
		})
	}
}
