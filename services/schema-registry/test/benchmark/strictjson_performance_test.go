// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// FuzzStrictJSONParse checks no-panic parsing, input ownership, and exact value round trips.
func FuzzStrictJSONParse(f *testing.F) {
	for _, seed := range jsonWorkloads() {
		_, err := strictjson.Parse(seed.Input)
		require.Equal(f, seed.Valid, err == nil, seed.Name)
		f.Add(seed.Input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		original := bytes.Clone(input)
		value, err := strictjson.Parse(input)
		require.Equal(t, original, input, "parser changed caller bytes")
		if err != nil {
			require.Nil(t, value, "failed parse exposed a partial value")
			return
		}
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		// Escaping can expand an accepted value beyond the input byte profile.
		if len(encoded) > schemaartifacts.MaxArtifactBytes {
			return
		}
		roundTrip, err := strictjson.Parse(encoded)
		require.NoError(t, err)
		require.Equal(t, value, roundTrip)
	})
}

// BenchmarkParseProfile checks allocation growth for accepted and rejected documents.
func BenchmarkParseProfile(b *testing.B) {
	for _, workload := range jsonBenchmarkWorkloads() {
		b.Run(workload.Name, func(b *testing.B) {
			_, err := strictjson.Parse(workload.Input)
			require.Equal(b, workload.Valid, err == nil)
			benchmarkJSONAllocations(b, workload.Input, 64, 4, func() {
				_, err := strictjson.Parse(workload.Input)
				if (err == nil) != workload.Valid {
					b.Fatal("parser result changed during benchmark")
				}
			})
		})
	}
}
