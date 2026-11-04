package drain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

func rowsWithBytes(bytes ...int64) []PendingRow {
	rows := make([]PendingRow, len(bytes))
	for i, b := range bytes {
		rows[i] = PendingRow{ID: int64(i + 1), ByteLen: b}
	}
	return rows
}

func TestNextBatchLen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rows     []PendingRow
		limits   store.BatchLimits
		wantN    int
		wantFull bool
	}{
		{
			name:     "exactly MaxRecords flushes full on count",
			rows:     rowsWithBytes(1, 1, 1, 1, 1),
			limits:   store.BatchLimits{MaxRecords: 5, MaxBytes: 0, MaxLinger: time.Second},
			wantN:    5,
			wantFull: true,
		},
		{
			name:     "fewer than MaxRecords and under bytes is not full (linger applies)",
			rows:     rowsWithBytes(1, 1),
			limits:   store.BatchLimits{MaxRecords: 10, MaxBytes: 20, MaxLinger: time.Second},
			wantN:    2,
			wantFull: false,
		},
		{
			name:     "MaxBytes=0 disables the byte trigger",
			rows:     rowsWithBytes(1000, 1000, 1000),
			limits:   store.BatchLimits{MaxRecords: 10, MaxBytes: 0, MaxLinger: time.Second},
			wantN:    3,
			wantFull: false,
		},
		{
			name:     "byte limit closes batch before the row that would exceed it",
			rows:     rowsWithBytes(3, 4, 5),
			limits:   store.BatchLimits{MaxRecords: 10, MaxBytes: 5, MaxLinger: time.Second},
			wantN:    1, // 3 fits; 3+4 would exceed 5, so close after first
			wantFull: true,
		},
		{
			name:     "single row larger than MaxBytes still ships as a batch of one",
			rows:     rowsWithBytes(100),
			limits:   store.BatchLimits{MaxRecords: 10, MaxBytes: 5, MaxLinger: time.Second},
			wantN:    1,
			wantFull: true,
		},
		{
			name:     "byte limit reached exactly including the row",
			rows:     rowsWithBytes(2, 3, 9),
			limits:   store.BatchLimits{MaxRecords: 10, MaxBytes: 5, MaxLinger: time.Second},
			wantN:    2, // 2+3 == 5 -> full
			wantFull: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, full := nextBatchLen(tc.rows, tc.limits)
			assert.Equal(t, tc.wantN, n)
			assert.Equal(t, tc.wantFull, full)
		})
	}
}

func TestToBatch_PreservesOrderAndIdentity(t *testing.T) {
	t.Parallel()
	rows := []PendingRow{
		{ID: 5, LogicalArtifactType: "ocg.telemetry.log", SchemaVersion: "2.4.0", Payload: []byte("a"), ContentType: "application/json"},
		{ID: 6, LogicalArtifactType: "ocg.telemetry.log", SchemaVersion: "2.4.0", Payload: []byte("b"), ContentType: "application/json"},
	}
	b := toBatch(42, rows)

	require.Equal(t, int64(42), b.ID)
	require.Len(t, b.Records, 2)
	assert.Equal(t, []byte("a"), b.Records[0].Payload)
	assert.Equal(t, []byte("b"), b.Records[1].Payload)
	assert.Equal(t, "ocg.telemetry.log", b.Records[0].Schema.LogicalArtifactType)
	assert.Equal(t, "2.4.0", b.Records[0].Schema.Version)
}

func TestIdsOf(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []int64{5, 6, 7}, idsOf(rowsWithBytesIDs(5, 6, 7)))
}

func rowsWithBytesIDs(ids ...int64) []PendingRow {
	rows := make([]PendingRow, len(ids))
	for i, id := range ids {
		rows[i] = PendingRow{ID: id}
	}
	return rows
}
