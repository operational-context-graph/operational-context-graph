// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package jsonworkloads constructs JSON inputs for black-box benchmarks and fuzz targets.
package jsonworkloads

import (
	"strings"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// Workload defines a named JSON input and its expected parsing outcome.
type Workload struct {
	Name  string
	Input []byte
	Valid bool
}

// Workloads returns fresh adversarial inputs and structural boundary cases.
// Program-shaped values exercise JSON structure, never program interpretation.
func Workloads() []Workload {
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
	return []Workload{
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

// BenchmarkWorkloads adds full-document byte boundaries to fresh shared workloads.
func BenchmarkWorkloads() []Workload {
	workloads := Workloads()
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
		Workload{"document-limit", []byte(document), true},
		Workload{"document-overflow", []byte(document + " "), false},
	)
}
