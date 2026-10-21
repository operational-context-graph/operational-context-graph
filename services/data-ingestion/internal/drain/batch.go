// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// nextBatchLen returns how many leading rows of rows (which must be in ascending
// id order) make up the next batch, and whether a hard limit closed it.
//
// A batch closes as soon as it reaches MaxRecords or MaxBytes.
// The 'full' attribute indicates if there is more place in the batch,
// after adding all pending rows from the input parameter.
//
// For non-empty input it returns at least 1 — a single row larger than MaxBytes
// still ships alone, so the byte limit can never stall a worker.
func nextBatchLen(rows []PendingRow, limits store.BatchLimits) (n int, full bool) {
	var bytes int64
	for i, row := range rows {
		// Adding this row would exceed MaxBytes and it is not the first row:
		// close the batch before it (M reached).
		if limits.MaxBytes > 0 && i > 0 && bytes+row.ByteLen > limits.MaxBytes {
			return i, true
		}
		bytes += row.ByteLen
		n = i + 1
		if n >= limits.MaxRecords {
			return n, true
		}
		// This row reached or exceeded MaxBytes (includes the oversized first
		// row): close including it.
		if limits.MaxBytes > 0 && bytes >= limits.MaxBytes {
			return n, true
		}
	}
	return n, false
}

// toBatch maps a closed set of pending rows plus the assigned batch_id into a
// store.Batch, preserving order.
func toBatch(batchID int64, rows []PendingRow) store.Batch {
	recs := make([]store.Record, len(rows))
	for i, r := range rows {
		recs[i] = store.Record{
			Schema:      ingest.SchemaKey{LogicalArtifactType: r.LogicalArtifactType, Version: r.SchemaVersion},
			Payload:     r.Payload,
			ContentType: r.ContentType,
		}
	}
	return store.Batch{ID: batchID, Records: recs}
}

// idsOf returns the outbox ids of rows, in order.
func idsOf(rows []PendingRow) []int64 {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}
