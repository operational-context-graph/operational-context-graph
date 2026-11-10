// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package fuzz

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/jsonworkloads"
)

// FuzzStrictJSONParse checks no-panic parsing, input ownership, and exact value round trips.
func FuzzStrictJSONParse(f *testing.F) {
	for _, seed := range jsonworkloads.Workloads() {
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
