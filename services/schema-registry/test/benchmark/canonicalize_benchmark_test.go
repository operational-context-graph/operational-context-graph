// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"testing"

	"github.com/gowebpki/jcs"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/jsonworkloads"
)

// BenchmarkCanonicalizeProfile includes the mandatory strict gate in every operation.
func BenchmarkCanonicalizeProfile(b *testing.B) {
	for _, workload := range jsonworkloads.BenchmarkWorkloads() {
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
