// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/drain"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// batchSeq is the sequence that assigns batch_id values (see migration 0002).
const batchSeq = "data_ingestion_outbox_batch_seq"

// Querier is the subset of a pgx pool the Reader needs. *pgxpool.Pool satisfies
// it. Keeping it narrow lets the Reader stay driver-light and testable.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Reader runs the drain's read-side operations against the outbox table. It is
// safe for concurrent use when backed by a *pgxpool.Pool.
type Reader struct {
	db Querier
}

// NewReader builds a Reader over the given pool-like querier.
func NewReader(db Querier) *Reader {
	return &Reader{db: db}
}

// ScanPending returns up to pageSize pending (unpinned) rows for storeID in ascending id (outbox) order.
func (r *Reader) ScanPending(ctx context.Context, storeID ingest.StoreID, pageSize int) ([]drain.PendingRow, error) {
	const q = `
SELECT id, logical_artifact_type, schema_version, payload, content_type, octet_length(payload::text)
FROM ` + Table + `
WHERE target_store = $1 AND batch_id IS NULL
ORDER BY id
LIMIT $2`

	rows, err := r.db.Query(ctx, q, string(storeID), pageSize)
	if err != nil {
		return nil, fmt.Errorf("outbox: scan pending for %q: %w", storeID, err)
	}
	defer rows.Close()

	var out []drain.PendingRow
	for rows.Next() {
		var p drain.PendingRow
		if err := rows.Scan(&p.ID, &p.LogicalArtifactType, &p.SchemaVersion, &p.Payload, &p.ContentType, &p.ByteLen); err != nil {
			return nil, fmt.Errorf("outbox: scan pending row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: scan pending for %q: %w", storeID, err)
	}
	return out, nil
}

// PinBatch stamps exactly the rows in ids (that are still pending) with one new
// batch_id and returns it. It reserves a single sequence value first, then
// assigns that value to every matched row, so the whole batch shares one batch_id.
func (r *Reader) PinBatch(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("outbox: pin batch: no ids")
	}

	// We need to separate reading the next batchID and pinning the rows in two querries.
	// A single-statement `SET batch_id = nextval(seq)` cannot be used because
	// nextval is VOLATILE and Postgres evaluates it once per row, which would give
	// each row a different batch_id.
	var batchID int64
	if err := r.db.QueryRow(ctx, `SELECT nextval($1)`, batchSeq).Scan(&batchID); err != nil {
		return 0, fmt.Errorf("outbox: reserve batch id: %w", err)
	}

	const q = `UPDATE ` + Table + ` SET batch_id = $1 WHERE id = ANY($2) AND batch_id IS NULL`
	tag, err := r.db.Exec(ctx, q, batchID, ids)
	if err != nil {
		return 0, fmt.Errorf("outbox: pin batch %d: %w", batchID, err)
	}
	if tag.RowsAffected() == 0 {
		return 0, fmt.Errorf("outbox: pin batch %d: no rows pinned", batchID)
	}
	return batchID, nil
}

// LoadPinned returns all pinned rows for storeID, ordered by batch_id then id,
// so the caller can group them into their original immutable batches and resend
// them after a restart.
func (r *Reader) LoadPinned(ctx context.Context, storeID ingest.StoreID) ([]drain.PendingRow, error) {
	const q = `
SELECT id, batch_id, logical_artifact_type, schema_version, payload, content_type, octet_length(payload::text)
FROM ` + Table + `
WHERE target_store = $1 AND batch_id IS NOT NULL
ORDER BY batch_id, id`

	rows, err := r.db.Query(ctx, q, string(storeID))
	if err != nil {
		return nil, fmt.Errorf("outbox: load pinned for %q: %w", storeID, err)
	}
	defer rows.Close()

	var out []drain.PendingRow
	for rows.Next() {
		var p drain.PendingRow
		if err := rows.Scan(&p.ID, &p.BatchID, &p.LogicalArtifactType, &p.SchemaVersion, &p.Payload, &p.ContentType, &p.ByteLen); err != nil {
			return nil, fmt.Errorf("outbox: scan pinned row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: load pinned for %q: %w", storeID, err)
	}
	return out, nil
}

// DeleteBatch removes exactly the rows stamped with batchID. It is scoped by
// batch_id so it never touches another batch's rows.
func (r *Reader) DeleteBatch(ctx context.Context, batchID int64) error {
	const q = `DELETE FROM ` + Table + ` WHERE batch_id = $1`
	if _, err := r.db.Exec(ctx, q, batchID); err != nil {
		return fmt.Errorf("outbox: delete batch %d: %w", batchID, err)
	}
	return nil
}

// DistinctPendingStores returns the distinct target_store values among pending (unpinned) rows.
func (r *Reader) DistinctPendingStores(ctx context.Context) ([]ingest.StoreID, error) {
	const q = `SELECT DISTINCT target_store FROM ` + Table + ` WHERE batch_id IS NULL`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("outbox: distinct pending stores: %w", err)
	}
	defer rows.Close()

	var out []ingest.StoreID
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("outbox: scan pending store: %w", err)
		}
		out = append(out, ingest.StoreID(s))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: distinct pending stores: %w", err)
	}
	return out, nil
}

// Compile-time assertion that the concrete reader satisfies the interface.
var _ drain.Outbox = (*Reader)(nil)
