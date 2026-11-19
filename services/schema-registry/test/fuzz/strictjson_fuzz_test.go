package fuzz

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
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

// FuzzParseUnicode checks that successful parsing preserves the original JSON's Unicode values under independent JCS validation.
func FuzzParseUnicode(f *testing.F) {
	for _, seed := range []string{
		`"\ud800"`, `"\udfff"`, `{"\ud800":true}`, `[{"nested":"\ud800"}]`,
		`"\ud83d\ude00"`, `"\\ud800"`, `"\u005cud800"`, `"�"`, `"\ufffd"`,
		`"\ufdd0"`, `"\udbff\udfff"`, `"\u"`, `"\`, `"é"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := strictjson.Parse([]byte(input))
		if err != nil {
			return
		}
		canonical, err := jcs.Transform([]byte(input))
		require.NoError(t, err, "successful parsing must not hide invalid Unicode")
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		roundTrip, err := jcs.Transform(encoded)
		require.NoError(t, err)
		assert.Equal(t, canonical, roundTrip)
	})
}
