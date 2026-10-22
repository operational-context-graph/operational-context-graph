// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"context"
	"sync"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// This file provides test utility functionality for other test.

// fakeOutbox is an in-memory Outbox for unit tests. It is safe for
// concurrent use so workers can run against it in goroutines.
type fakeOutbox struct {
	mu   sync.Mutex
	rows []fakeRow
	seq  int64

	scanErr     error
	pinErr      error
	deleteErr   error
	loadErr     error
	distinctErr error
}

type fakeRow struct {
	store   ingest.StoreID
	id      int64
	batchID int64 // 0 == pending
	bytes   int64
}

// seed appends n pending rows for storeID, each with the given byte length, and
// returns their ids.
func (f *fakeOutbox) seed(storeID ingest.StoreID, n int, bytesEach int64) []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		f.seq++
		id := f.seq
		f.rows = append(f.rows, fakeRow{store: storeID, id: id, bytes: bytesEach})
		ids[i] = id
	}
	return ids
}

func (f *fakeOutbox) pendingCount(storeID ingest.StoreID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := 0
	for _, r := range f.rows {
		if r.store == storeID && r.batchID == 0 {
			c++
		}
	}
	return c
}

func (f *fakeOutbox) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeOutbox) toPendingRow(r fakeRow) PendingRow {
	var batchID *int64 // nil == pending, matching the production sentinel
	if r.batchID != 0 {
		v := r.batchID
		batchID = &v
	}
	return PendingRow{
		ID:                  r.id,
		BatchID:             batchID,
		LogicalArtifactType: "ocg.telemetry.log",
		SchemaVersion:       "2.4.0",
		Payload:             []byte(`{"k":"v"}`),
		ContentType:         "application/json",
		ByteLen:             r.bytes,
	}
}

func (f *fakeOutbox) ScanPending(_ context.Context, storeID ingest.StoreID, pageSize int) ([]PendingRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	var out []PendingRow
	for _, r := range f.rows {
		if r.store == storeID && r.batchID == 0 {
			out = append(out, f.toPendingRow(r))
			if len(out) >= pageSize {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeOutbox) PinBatch(_ context.Context, ids []int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pinErr != nil {
		return 0, f.pinErr
	}
	if len(ids) == 0 {
		return 0, context.Canceled
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	f.seq++
	batchID := f.seq
	pinned := 0
	for i := range f.rows {
		if want[f.rows[i].id] && f.rows[i].batchID == 0 {
			f.rows[i].batchID = batchID
			pinned++
		}
	}
	if pinned == 0 {
		return 0, context.Canceled
	}
	return batchID, nil
}

func (f *fakeOutbox) LoadPinned(_ context.Context, storeID ingest.StoreID) ([]PendingRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	// Collect pinned rows for the store, ordered by (batchID, id). The backing
	// slice is already in id order and pins are monotonic, so a stable gather
	// per ascending batchID preserves the contract.
	var out []PendingRow
	seen := map[int64]bool{}
	for _, r := range f.rows {
		if r.store == storeID && r.batchID != 0 {
			seen[r.batchID] = true
		}
	}
	// Order batch ids.
	var batchIDs []int64
	for b := range seen {
		batchIDs = append(batchIDs, b)
	}
	// insertion sort (small n)
	for i := 1; i < len(batchIDs); i++ {
		for j := i; j > 0 && batchIDs[j-1] > batchIDs[j]; j-- {
			batchIDs[j-1], batchIDs[j] = batchIDs[j], batchIDs[j-1]
		}
	}
	for _, b := range batchIDs {
		for _, r := range f.rows {
			if r.store == storeID && r.batchID == b {
				out = append(out, f.toPendingRow(r))
			}
		}
	}
	return out, nil
}

func (f *fakeOutbox) DeleteBatch(_ context.Context, batchID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	kept := f.rows[:0]
	for _, r := range f.rows {
		if r.batchID != batchID {
			kept = append(kept, r)
		}
	}
	f.rows = kept
	return nil
}

func (f *fakeOutbox) DistinctPendingStores(_ context.Context) ([]ingest.StoreID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.distinctErr != nil {
		return nil, f.distinctErr
	}
	seen := map[ingest.StoreID]bool{}
	var out []ingest.StoreID
	for _, r := range f.rows {
		if r.batchID == 0 && !seen[r.store] {
			seen[r.store] = true
			out = append(out, r.store)
		}
	}
	return out, nil
}

// fakeWriter is a scriptable StoreWriter. writeFn decides the result per call;
// every delivered batch is recorded.
type fakeWriter struct {
	id      ingest.StoreID
	limits  store.BatchLimits
	writeFn func(call int, b store.Batch) error

	mu    sync.Mutex
	calls []store.Batch
}

func (w *fakeWriter) StoreID() ingest.StoreID   { return w.id }
func (w *fakeWriter) Limits() store.BatchLimits { return w.limits }

func (w *fakeWriter) Write(ctx context.Context, b store.Batch) error {
	w.mu.Lock()
	n := len(w.calls)
	w.calls = append(w.calls, b)
	w.mu.Unlock()
	if w.writeFn == nil {
		return nil
	}
	return w.writeFn(n, b)
}

func (w *fakeWriter) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func (w *fakeWriter) callAt(i int) store.Batch {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[i]
}

// fastConfig returns a Config with small timings for tests.
func fastConfig() Config {
	return Config{
		PollInterval:   2 * time.Millisecond,
		BackoffInitial: time.Millisecond,
		BackoffMax:     8 * time.Millisecond,
		BackoffJitter:  0.1,
		MaxRetries:     0,
		PageSize:       100,
	}
}
