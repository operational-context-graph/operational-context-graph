// Package drain implements the asynchronous delivery half of Data Ingestion: a
// single process that reads pending rows from the outbox, forms immutable
// batches, pins them, delivers each through the owning store's StoreWriter, and
// removes the rows only after the store acknowledges them (at-least-once). It
// runs one worker per target_store so an overloaded store pauses only its own
// worker.
package drain

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// PendingRow is one outbox row read by the drain. ByteLen is octet_length of
// the stored payload, used for the drain's MaxBytes accounting. BatchID is nil
// for a pending (unpinned) row and holds the pinned batch_id for rows returned
// by LoadPinned.
type PendingRow struct {
	ID                  int64
	BatchID             *int64
	LogicalArtifactType string
	SchemaVersion       string
	Payload             []byte
	ContentType         string
	ByteLen             int64
}

// Outbox is the read-side the drain depends on.
type Outbox interface {
	// ScanPending returns up to pageSize pending rows for storeID in ascending id order.
	ScanPending(ctx context.Context, storeID ingest.StoreID, pageSize int) ([]PendingRow, error)
	// PinBatch stamps the given ids with one new batch_id and returns it.
	PinBatch(ctx context.Context, ids []int64) (int64, error)
	// LoadPinned returns all already-pinned rows for storeID ordered by batch_id then id.
	LoadPinned(ctx context.Context, storeID ingest.StoreID) ([]PendingRow, error)
	// DeleteBatch removes the rows stamped with batchID.
	DeleteBatch(ctx context.Context, batchID int64) error
	// DistinctPendingStores returns the distinct target_store values among pending rows.
	DistinctPendingStores(ctx context.Context) ([]ingest.StoreID, error)
}
