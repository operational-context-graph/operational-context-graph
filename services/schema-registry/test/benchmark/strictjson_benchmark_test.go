// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/jsonworkloads"
)

func BenchmarkStrictJSONParse(b *testing.B) {
	input := []byte(`{"metadata":{"name":"device"},"values":[1,2,3],"active":true}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		if _, err := strictjson.Parse(input); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseProfile checks allocation growth for accepted and rejected documents.
func BenchmarkParseProfile(b *testing.B) {
	for _, workload := range jsonworkloads.BenchmarkWorkloads() {
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
