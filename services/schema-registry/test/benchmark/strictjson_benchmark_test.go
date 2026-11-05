// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
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
