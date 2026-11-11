package jsonworkloads

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

func TestWorkloads(t *testing.T) {
	workloads := Workloads()
	require.NotEmpty(t, workloads)
	names := make(map[string]bool)
	for _, workload := range workloads {
		t.Run(workload.Name, func(t *testing.T) {
			require.NotEmpty(t, workload.Name)
			require.False(t, names[workload.Name], "duplicate workload name")
			names[workload.Name] = true
			_, err := strictjson.Parse(workload.Input)
			require.Equal(t, workload.Valid, err == nil)
		})
	}
}

func TestBenchmarkWorkloads(t *testing.T) {
	base := Workloads()
	workloads := BenchmarkWorkloads()
	require.Len(t, workloads, len(base)+2)
	require.Equal(t, base, workloads[:len(base)])
	limit, overflow := workloads[len(base)], workloads[len(base)+1]
	require.Equal(t, "document-limit", limit.Name)
	require.True(t, limit.Valid)
	require.Len(t, limit.Input, schemaartifacts.MaxArtifactBytes)
	_, err := strictjson.Parse(limit.Input)
	require.NoError(t, err)
	require.Equal(t, "document-overflow", overflow.Name)
	require.False(t, overflow.Valid)
	require.Equal(t, append(bytes.Clone(limit.Input), ' '), overflow.Input)
	_, err = strictjson.Parse(overflow.Input)
	require.Error(t, err)
}

func TestWorkloadsReturnFreshInputs(t *testing.T) {
	for name, build := range map[string]func() []Workload{
		"shared": Workloads, "benchmark": BenchmarkWorkloads,
	} {
		t.Run(name, func(t *testing.T) {
			original := build()
			expected := slices.Clone(original)
			for i := range expected {
				expected[i].Input = bytes.Clone(expected[i].Input)
			}
			changed := build()
			require.Equal(t, expected, changed)
			for i := range changed {
				changed[i].Name = "changed"
				changed[i].Valid = !changed[i].Valid
				if len(changed[i].Input) > 0 {
					changed[i].Input[0] ^= 0xff
				}
			}
			require.Equal(t, expected, original, "mutation must not change another caller's existing workload data")
			require.Equal(t, expected, build(), "mutation must not change subsequent workload data")
		})
	}
}
