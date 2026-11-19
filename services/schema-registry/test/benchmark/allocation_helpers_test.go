package benchmark

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

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
