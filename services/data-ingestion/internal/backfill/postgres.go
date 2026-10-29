// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxQuerier is the subset of *pgxpool.Pool that PostgresBackfiller needs.
// The interface enables test doubles without a real database connection.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// PostgresBackfiller implements Backfiller against a PostgreSQL
// data_ingestion_watermarks table. It is stateless beyond the db reference
// and safe for concurrent use.
type PostgresBackfiller struct {
	db pgxQuerier
}


// NewPostgresBackfiller constructs a PostgresBackfiller. Returns a non-nil
// error when pool is nil so wiring mistakes surface at startup.
func NewPostgresBackfiller(pool *pgxpool.Pool) (*PostgresBackfiller, error) {
	if pool == nil {
		return nil, fmt.Errorf("backfill: pool must not be nil")
	}
	return &PostgresBackfiller{db: pool}, nil
}

// GetWatermark returns the last committed watermark for the (connectorID,
// partition) pair. Returns (nil, false, nil) when no row exists.
func (backfiller *PostgresBackfiller) GetWatermark(ctx context.Context, connectorID, partition string) (map[string]any, bool, error) {
	const q = `SELECT watermark FROM data_ingestion_watermarks WHERE connector_id=$1 AND partition_key=$2`

	var raw []byte
	err := backfiller.db.QueryRow(ctx, q, connectorID, partition).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("backfill: get watermark: %w", err)
	}
	if raw == nil {
		return nil, false, nil
	}

	var wm map[string]any
	if err := json.Unmarshal(raw, &wm); err != nil {
		return nil, false, fmt.Errorf("backfill: get watermark: unmarshal: %w", err)
	}
	return wm, true, nil
}

// StartGap opens a gap for the (connectorID, partition) pair. Idempotent.
// live_frontier is cleared on every open so a stale frontier from a previous
// incomplete gap cycle cannot corrupt the next CloseGap via COALESCE.
func (backfiller *PostgresBackfiller) StartGap(ctx context.Context, connectorID, partition string) error {
	const q = `
INSERT INTO data_ingestion_watermarks (connector_id, partition_key, gap_open, updated_at)
VALUES ($1, $2, true, now())
ON CONFLICT (connector_id, partition_key)
DO UPDATE SET gap_open = true, live_frontier = NULL, updated_at = now()`

	if _, err := backfiller.db.Exec(ctx, q, connectorID, partition); err != nil {
		return fmt.Errorf("backfill: start gap: %w", err)
	}
	return nil
}

// CloseGap closes an open gap. If a live_frontier was accumulated, the
// committed watermark jumps to it. No-op when no gap is open.
func (backfiller *PostgresBackfiller) CloseGap(ctx context.Context, connectorID, partition string) error {
	const q = `
UPDATE data_ingestion_watermarks
   SET gap_open      = false,
       watermark     = COALESCE(live_frontier, watermark),
       live_frontier = NULL,
       updated_at    = now()
 WHERE connector_id = $1 AND partition_key = $2 AND gap_open = true`

	if _, err := backfiller.db.Exec(ctx, q, connectorID, partition); err != nil {
		return fmt.Errorf("backfill: close gap: %w", err)
	}
	return nil
}

// AdvanceLive updates the committed watermark from the live stream. When a
// gap is open it stores the watermark as live_frontier instead. No-op when
// connectorID or watermark is empty. An empty partition key is valid — it
// represents a global per-connector watermark (no partition).
//
// Callers MUST invoke AdvanceLive only after outbox.Write returns nil.
func (backfiller *PostgresBackfiller) AdvanceLive(ctx context.Context, connectorID, partition string, watermark map[string]any) error {
	if connectorID == "" || len(watermark) == 0 {
		return nil
	}

	raw, err := json.Marshal(watermark)
	if err != nil {
		return fmt.Errorf("backfill: advance live: marshal: %w", err)
	}

	// Single upsert: when no gap is open update committed watermark;
	// when a gap is open update live_frontier only.
	const q = `
INSERT INTO data_ingestion_watermarks (connector_id, partition_key, watermark, gap_open, updated_at)
VALUES ($1, $2, $3, false, now())
ON CONFLICT (connector_id, partition_key) DO UPDATE
   SET watermark     = CASE WHEN data_ingestion_watermarks.gap_open THEN data_ingestion_watermarks.watermark ELSE EXCLUDED.watermark END,
       live_frontier = CASE WHEN data_ingestion_watermarks.gap_open THEN EXCLUDED.watermark                  ELSE data_ingestion_watermarks.live_frontier END,
       updated_at    = now()`

	if _, err := backfiller.db.Exec(ctx, q, connectorID, partition, raw); err != nil {
		return fmt.Errorf("backfill: advance live: %w", err)
	}
	return nil
}

// AdvanceBackfill advances the committed watermark from the backfill channel
// only while a gap is open. Silent no-op when no gap is open or when
// connectorID, partition, or watermark is empty.
//
// Callers MUST invoke AdvanceBackfill only after outbox.Write returns nil.
func (backfiller *PostgresBackfiller) AdvanceBackfill(ctx context.Context, connectorID, partition string, watermark map[string]any) error {
	if connectorID == "" || partition == "" || len(watermark) == 0 {
		return nil
	}

	raw, err := json.Marshal(watermark)
	if err != nil {
		return fmt.Errorf("backfill: advance backfill: marshal: %w", err)
	}

	const q = `
UPDATE data_ingestion_watermarks
   SET watermark  = $3,
       updated_at = now()
 WHERE connector_id = $1 AND partition_key = $2 AND gap_open = true`

	if _, err := backfiller.db.Exec(ctx, q, connectorID, partition, raw); err != nil {
		return fmt.Errorf("backfill: advance backfill: %w", err)
	}
	return nil
}

// IsGapOpen reports whether a gap is currently open for the (connectorID,
// partition) pair. Returns (false, nil) when no row exists.
func (backfiller *PostgresBackfiller) IsGapOpen(ctx context.Context, connectorID, partition string) (bool, error) {
	const q = `SELECT gap_open FROM data_ingestion_watermarks WHERE connector_id=$1 AND partition_key=$2`

	var gapOpen bool
	err := backfiller.db.QueryRow(ctx, q, connectorID, partition).Scan(&gapOpen)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("backfill: is gap open: %w", err)
	}
	return gapOpen, nil
}
