// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// jsonWorkload defines a named JSON input and its expected parsing outcome.
type jsonWorkload struct {
	Name  string
	Input []byte
	Valid bool
}

// jsonWorkloads returns adversarial inputs and structural boundary cases.
// Program-shaped values exercise JSON structure, never program interpretation.
func jsonWorkloads() []jsonWorkload {
	const transformation = `{"cardinality":"one","programDigest":"sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","programRef":"shared.bin","targetSchema":{"id":"device","revision":"target-1"}}`
	transformations := func(count int) string {
		return `{"programs":{"transformations":[` + strings.Repeat(transformation+",", count-1) + transformation + `]}}`
	}
	object := func(count int) string {
		members := make([]string, count)
		for i := range members {
			// Distinct names exercise the member-count limit rather than duplicates.
			members[i] = `"` + strings.Repeat("a", i+1) + `":null`
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	return []jsonWorkload{
		{"empty", nil, false},
		{"empty-object", []byte(`{}`), true},
		{"empty-array", []byte(`[]`), true},
		{"null", []byte(`null`), true},
		{"malformed-utf8", []byte{'"', 0xff, '"'}, false},
		{"duplicate", []byte(`{"a":0,"a":1}`), false},
		{"escaped-duplicate", []byte(`{"programs":{"condition":null,"\u0063ondition":{}}}`), false},
		{"depth-limit", []byte(strings.Repeat("[", schemaartifacts.MaxJSONDepth) + "0" + strings.Repeat("]", schemaartifacts.MaxJSONDepth)), true},
		{"depth-overflow", []byte(strings.Repeat("[", schemaartifacts.MaxJSONDepth+1) + "0" + strings.Repeat("]", schemaartifacts.MaxJSONDepth+1)), false},
		{"extreme-depth", []byte(strings.Repeat("[", 4096) + "0" + strings.Repeat("]", 4096)), false},
		{"member-limit", []byte(object(schemaartifacts.MaxObjectMembers)), true},
		{"member-overflow", []byte(object(schemaartifacts.MaxObjectMembers + 1)), false},
		{"one-transformation", []byte(transformations(1)), true},
		{"transformation-limit", []byte(transformations(schemaartifacts.MaxArrayEntries)), true},
		{"transformation-overflow", []byte(transformations(schemaartifacts.MaxArrayEntries + 1)), false},
		{"null-condition", []byte(`{"programs":{"condition":null,"transformations":[]}}`), true},
		{"string-limit", []byte(`"` + strings.Repeat("é", schemaartifacts.MaxJSONStringBytes/2) + `"`), true},
		{"string-overflow", []byte(`"` + strings.Repeat("a", schemaartifacts.MaxJSONStringBytes+1) + `"`), false},
		{"escaped-string-limit", []byte(`"` + strings.Repeat(`\u0061`, schemaartifacts.MaxJSONStringBytes) + `"`), true},
		{"numeric-boundaries", []byte(`[-9007199254740991,9007199254740991,-0,1.25e-3,5e-324]`), true},
		{"integer-overflow", []byte(`9007199254740992`), false},
		{"negative-integer-overflow", []byte(`-9007199254740992`), false},
		{"decimal-integer-overflow", []byte(`9007199254740992.0`), false},
		{"exponent-integer-overflow", []byte(`9007199254740992e0`), false},
		{"exponent-overflow", []byte(`1e10000`), false},
		{"huge-number", []byte(strings.Repeat("9", 4096)), false},
		{"unicode-order", []byte(`{"\ue000":"é","\ud800\udc00":"e\u0301"}`), true},
	}
}

// jsonBenchmarkWorkloads adds full-document byte boundaries to the shared workloads.
func jsonBenchmarkWorkloads() []jsonWorkload {
	workloads := jsonWorkloads()
	const count = 16
	// Account for brackets, commas, and quotes before distributing string bytes.
	contentBytes := schemaartifacts.MaxArtifactBytes - (3*count + 1)
	items := make([]string, count)
	for i := range items {
		size := contentBytes / count
		if i < contentBytes%count {
			size++
		}
		items[i] = `"` + strings.Repeat("a", size) + `"`
	}
	document := "[" + strings.Join(items, ",") + "]"
	return append(workloads,
		jsonWorkload{"document-limit", []byte(document), true},
		jsonWorkload{"document-overflow", []byte(document + " "), false},
	)
}

// benchmarkJSONAllocations measures an operation and enforces allocation budgets.
// Setup and input construction stay outside the measured operation.
func benchmarkJSONAllocations(b *testing.B, input []byte, byteFactor, allocFactor uint64, operation func()) {
	b.Helper()
	iterations := b.N
	if iterations <= 0 {
		b.Fatal("benchmark iteration count must be positive")
		return
	}
	iterationCount := uint64(iterations)
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	b.StopTimer()
	b.ResetTimer()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b.StartTimer()
	for range iterations {
		operation()
	}
	b.StopTimer()
	runtime.ReadMemStats(&after)
	// Process-wide counters include runtime noise. Fixed headroom avoids brittle
	// platform-specific thresholds; these are regression budgets, not heap limits.
	byteBudget := uint64(1<<20) + byteFactor*uint64(len(input))
	allocBudget := uint64(4096) + allocFactor*uint64(len(input))
	require.LessOrEqual(b, (after.TotalAlloc-before.TotalAlloc)/iterationCount, byteBudget, "bytes/op budget")
	require.LessOrEqual(b, (after.Mallocs-before.Mallocs)/iterationCount, allocBudget, "allocs/op budget")
}
