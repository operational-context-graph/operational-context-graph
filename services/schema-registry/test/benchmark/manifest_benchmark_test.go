// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"bytes"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared"
)

func BenchmarkParseCanonicalManifest(b *testing.B) {
	data := bytes.TrimSuffix(shared.ReadFixture(b, "valid-initial.json"), []byte("\n"))
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		_, err := manifest.Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
