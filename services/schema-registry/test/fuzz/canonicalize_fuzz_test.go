package fuzz

import (
	"bytes"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/jsonworkloads"
)

// FuzzCanonicalize exercises JCS only after the production strict parsing gate.
// Arbitrary program-shaped metadata remains JSON data, not executable programs.
func FuzzCanonicalize(f *testing.F) {
	f.Add(bytes.TrimSuffix(corpus.ReadFixture(f, "valid-initial.json"), []byte("\n")))
	for _, seed := range jsonworkloads.Workloads() {
		f.Add(seed.Input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if _, err := strictjson.Parse(input); err != nil {
			return
		}
		original := bytes.Clone(input)
		canonical, err := jcs.Transform(input)
		require.Equal(t, original, input)
		if err != nil {
			// JCS can reject values beyond the strict parser's syntax checks.
			return
		}
		again, err := jcs.Transform(canonical)
		require.NoError(t, err)
		require.Equal(t, canonical, again, "canonicalization is not idempotent")
	})
}
