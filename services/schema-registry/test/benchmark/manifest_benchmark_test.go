// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"bytes"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
)

// BenchmarkParseManifestProfile measures the complete authoritative manifest parser.
func BenchmarkParseManifestProfile(b *testing.B) {
	input := bytes.TrimSuffix(corpus.ReadFixture(b, "valid-initial.json"), []byte("\n"))
	benchmarkJSONAllocations(b, input, 128, 8, func() {
		if _, err := manifest.Parse(input); err != nil {
			b.Fatal(err)
		}
	})
}
