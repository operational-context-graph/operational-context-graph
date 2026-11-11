// Package corpus loads checked-in Schema Registry test corpora without depending on production packages.
package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Corpus describes release recipes and their exact byte sources, not constructed release fixtures.
type Corpus struct {
	Format, Origin, Schema string
	Source                 Source
	Releases               []Release
}

// Source identifies the authoring snapshot declared by the corpus.
type Source struct {
	Repository, Commit string
}

// Release describes selected artifact recipes and an optional predecessor recipe.
type Release struct {
	Name, Parent, Version, Compatibility string
	Artifacts                            []Artifact
}

// Artifact describes fixture metadata, payload sources, resources, and release relationships.
type Artifact struct {
	Kind, Type, Revision, Class, TargetVersion, Payload, PayloadFile string
	Resources                                                        []Resource
	Dependencies, Contexts, Stores                                   []string
}

// Resource describes an exact byte source decoded from padded Base64 corpus content.
type Resource struct {
	Name, Role, MediaType string
	Content               []byte
}

// FailureCase describes an invalid fixture recipe and its expected validation outcome.
// Value retains encoding/json's standard representation, including float64 numbers.
type FailureCase struct {
	ID, Rule, Phase, Classification, ErrorContains string
	Release, Target, Artifact, Path, Operation     string
	Value                                          any
}

// LoadCorpus returns a fresh copy of the checked-in release corpus definitions.
func LoadCorpus(t testing.TB) Corpus {
	t.Helper()
	var corpus Corpus
	require.NoError(t, readJSON("release-set.json", &corpus))
	require.Equal(t, "schema-registry-release-corpus-1", corpus.Format)
	return corpus
}

// LoadFailures returns fresh invalid-fixture definitions without applying their mutations or validating releases.
func LoadFailures(t testing.TB) []FailureCase {
	t.Helper()
	var corpus struct {
		Format string
		Cases  []FailureCase
	}
	require.NoError(t, readJSON("release-failures.json", &corpus))
	require.Equal(t, "schema-registry-invalid-release-corpus-1", corpus.Format)
	require.NotEmpty(t, corpus.Cases)
	return corpus.Cases
}

// ReadFixture reads a file relative to test/fixtures/manifest, independent of the test's working directory.
// It returns fresh bytes without trimming newlines, decoding text, or embedding fixture files in the test binary.
// Tests MUST run from a source checkout with source paths retained in runtime caller information.
func ReadFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := readFixture(name)
	require.NoError(t, err)
	return data
}

// readFixture anchors file access to this source file and rejects paths outside the fixture directory.
func readFixture(name string) ([]byte, error) {
	if !filepath.IsLocal(name) {
		return nil, fmt.Errorf("fixture path must remain inside test/fixtures/manifest: %q", name)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("cannot locate corpus loader source")
	}
	// #nosec G304 -- the name is local to the checked-in fixture directory.
	return os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "manifest", name))
}

// readJSON decodes corpus metadata without applying production format or release validation.
func readJSON(name string, value any) error {
	data, err := readFixture(name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode corpus %s: %w", name, err)
	}
	return nil
}
