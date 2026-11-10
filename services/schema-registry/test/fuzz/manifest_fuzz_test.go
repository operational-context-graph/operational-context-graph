// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package fuzz

import (
	"bytes"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/jsonworkloads"
)

// FuzzManifestParse verifies canonical manifest acceptance without changing submitted bytes.
func FuzzManifestParse(f *testing.F) {
	initial := bytes.TrimSuffix(corpus.ReadFixture(f, "valid-initial.json"), []byte("\n"))
	f.Add(initial)
	for _, seed := range jsonworkloads.Workloads() {
		f.Add(seed.Input)
		if !seed.Valid {
			continue
		}
		// Valid extension values also reach canonicalization and typed manifest parsing.
		wrapped := append([]byte(`{"extensions":{"org.example.profile":`), seed.Input...)
		wrapped = append(wrapped, []byte(`},`)...)
		wrapped = append(wrapped, initial[1:]...)
		if _, err := strictjson.Parse(wrapped); err != nil {
			continue // The enclosing manifest can exceed the depth boundary.
		}
		canonical, err := jcs.Transform(wrapped)
		require.NoError(f, err)
		_, err = manifest.Parse(canonical)
		require.NoError(f, err, seed.Name)
		f.Add(canonical)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		original := bytes.Clone(input)
		parsed, err := manifest.Parse(input)
		require.Equal(t, original, input)
		if err != nil {
			require.Empty(t, parsed.Raw)
			return
		}
		require.Equal(t, input, parsed.Raw)
		canonical, err := jcs.Transform(parsed.Raw)
		require.NoError(t, err)
		require.Equal(t, input, canonical)
		again, err := manifest.Parse(canonical)
		require.NoError(t, err)
		require.Equal(t, parsed, again)
	})
}
