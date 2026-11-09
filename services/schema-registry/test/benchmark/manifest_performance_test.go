// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"bytes"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared"
)

// FuzzManifestParse verifies canonical manifest acceptance without changing submitted bytes.
func FuzzManifestParse(f *testing.F) {
	initial := bytes.TrimSuffix(shared.ReadFixture(f, "valid-initial.json"), []byte("\n"))
	f.Add(initial)
	for _, seed := range jsonWorkloads() {
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

// FuzzCanonicalize exercises JCS only after the production strict parsing gate.
// Arbitrary program-shaped metadata remains JSON data, not executable programs.
func FuzzCanonicalize(f *testing.F) {
	f.Add(bytes.TrimSuffix(shared.ReadFixture(f, "valid-initial.json"), []byte("\n")))
	for _, seed := range jsonWorkloads() {
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

// BenchmarkCanonicalizeProfile includes the mandatory strict gate in every operation.
func BenchmarkCanonicalizeProfile(b *testing.B) {
	for _, workload := range jsonBenchmarkWorkloads() {
		b.Run(workload.Name, func(b *testing.B) {
			benchmarkJSONAllocations(b, workload.Input, 128, 8, func() {
				_, err := strictjson.Parse(workload.Input)
				if (err == nil) != workload.Valid {
					b.Fatal("strict gate result changed during benchmark")
				}
				if err != nil {
					return
				}
				if _, err := jcs.Transform(workload.Input); err != nil {
					b.Fatal(err)
				}
			})
		})
	}
}

// BenchmarkParseManifestProfile measures the complete authoritative manifest parser.
func BenchmarkParseManifestProfile(b *testing.B) {
	input := bytes.TrimSuffix(shared.ReadFixture(b, "valid-initial.json"), []byte("\n"))
	benchmarkJSONAllocations(b, input, 128, 8, func() {
		if _, err := manifest.Parse(input); err != nil {
			b.Fatal(err)
		}
	})
}
