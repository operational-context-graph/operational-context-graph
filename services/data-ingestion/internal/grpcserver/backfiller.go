// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/backfill"
)

var _ Backfiller = (*backfill.PostgresBackfiller)(nil) // compile-time interface check

// Backfiller is the subset of the watermark store needed by the gRPC handlers.
// All state is persisted to PostgreSQL; there is no in-memory caching.
// Implementations MUST be safe for concurrent use by multiple goroutines.
//
// Commit-ordering invariant: callers MUST invoke AdvanceLive and
// AdvanceBackfill only after outbox.Write returns nil. Backfiller does not
// enforce this ordering internally; violating it risks advancing the watermark
// past a record that was never durably written.
type Backfiller interface {
	// GetWatermark returns the last committed watermark for the
	// (connectorID, partition) pair. Returns (nil, nil) when no watermark has
	// been recorded. Returns a non-nil error on storage failure.
	GetWatermark(ctx context.Context, connectorID, partition string) (watermark map[string]any, err error)

	// StartGap records that a gap exists for the (connectorID, partition)
	// pair, disabling live-stream watermark advancement until CloseGap is
	// called. The operation is idempotent: calling it when a gap is already
	// open is a no-op.
	StartGap(ctx context.Context, connectorID, partition string) error

	// CloseGap closes an open gap for the (connectorID, partition) pair. On
	// close, the committed watermark jumps to the live frontier accumulated
	// during the gap; if no live event arrived, the watermark stays at the
	// last backfill position. CloseGap is a no-op when no gap is open.
	CloseGap(ctx context.Context, connectorID, partition string) error

	// AdvanceLive updates the committed watermark from the live stream when
	// no gap is open; when a gap is open it records the watermark as
	// live_frontier only. The operation is a no-op when connectorID or
	// watermark is empty. An empty partition key is valid — it represents a
	// global per-connector watermark (no partition).
	//
	// Precondition: callers MUST invoke AdvanceLive only after
	// outbox.Write returns nil.
	AdvanceLive(ctx context.Context, connectorID, partition string, watermark map[string]any) error

	// AdvanceBackfill advances the committed watermark from the backfill
	// channel only while a gap is open. The operation is a silent no-op when
	// no gap is open or when connectorID, partition, or watermark is empty.
	//
	// Precondition: callers MUST invoke AdvanceBackfill only after
	// outbox.Write returns nil.
	AdvanceBackfill(ctx context.Context, connectorID, partition string, watermark map[string]any) error

	// IsGapOpen reports whether a gap is currently open for the
	// (connectorID, partition) pair. Returns (false, nil) when no row exists
	// (no gap has been started). Returns a non-nil error on storage failure.
	IsGapOpen(ctx context.Context, connectorID, partition string) (bool, error)
}
