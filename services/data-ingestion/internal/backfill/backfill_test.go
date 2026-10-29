// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── fake DB ──────────────────────────────────────────────────────────────────

// fakeRow implements pgx.Row. Scan copies preset values into the dest pointers.
type fakeRow struct {
	scanErr error
	values  []any
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	for i, d := range dest {
		if i >= len(r.values) {
			break
		}
		switch v := d.(type) {
		case *[]byte:
			if r.values[i] == nil {
				*v = nil
			} else {
				*v = r.values[i].([]byte)
			}
		case *bool:
			*v = r.values[i].(bool)
		}
	}
	return nil
}

// fakeDB is a test double for the pgxQuerier interface.
type fakeDB struct {
	mu       sync.Mutex
	execErr  error
	row      *fakeRow
	execSQL  []string
	execArgs [][]any
}

func (d *fakeDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return d.row
}

func (d *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	d.execSQL = append(d.execSQL, sql)
	d.execArgs = append(d.execArgs, args)
	d.mu.Unlock()
	return pgconn.CommandTag{}, d.execErr
}

func newBackfiller(d pgxQuerier) *PostgresBackfiller {
	return &PostgresBackfiller{db: d}
}

// ── constructor ───────────────────────────────────────────────────────────────

func TestNewPostgresBackfiller_NilPool(t *testing.T) {
	b, err := NewPostgresBackfiller(nil)
	require.Error(t, err)
	assert.Nil(t, b)
}

// ── GetWatermark ─────────────────────────────────────────────────────────────

func TestGetWatermark_NoRow(t *testing.T) {
	d := &fakeDB{row: &fakeRow{scanErr: pgx.ErrNoRows}}
	b := newBackfiller(d)

	wm, found, err := b.GetWatermark(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, wm)
}

func TestGetWatermark_NullWatermark(t *testing.T) {
	// Row exists but watermark column is NULL.
	d := &fakeDB{row: &fakeRow{values: []any{nil}}}
	b := newBackfiller(d)

	wm, found, err := b.GetWatermark(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, wm)
}

func TestGetWatermark_CommittedWatermark(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"offset": float64(42)})
	d := &fakeDB{row: &fakeRow{values: []any{raw}}}
	b := newBackfiller(d)

	wm, found, err := b.GetWatermark(context.Background(), "conn1", `{"p":1}`)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]any{"offset": float64(42)}, wm)
}

func TestGetWatermark_DBError(t *testing.T) {
	boom := errors.New("db error")
	d := &fakeDB{row: &fakeRow{scanErr: boom}}
	b := newBackfiller(d)

	_, _, err := b.GetWatermark(context.Background(), "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestGetWatermark_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{row: &fakeRow{scanErr: context.Canceled}}
	b := newBackfiller(d)

	_, _, err := b.GetWatermark(ctx, "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ── StartGap ─────────────────────────────────────────────────────────────────

func TestStartGap_Success(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.StartGap(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestStartGap_Idempotent(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	require.NoError(t, b.StartGap(context.Background(), "conn1", "{}"))
	require.NoError(t, b.StartGap(context.Background(), "conn1", "{}"))
	assert.Len(t, d.execSQL, 2)
}

func TestStartGap_ClearsLiveFrontierOnConflict(t *testing.T) {
	// ON CONFLICT must include live_frontier = NULL so a stale frontier from
	// a previous incomplete gap cycle cannot corrupt CloseGap via COALESCE.
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.StartGap(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	require.Len(t, d.execSQL, 1)
	assert.Contains(t, d.execSQL[0], "live_frontier = NULL")
}

func TestStartGap_DBError(t *testing.T) {
	boom := errors.New("db error")
	d := &fakeDB{execErr: boom}
	b := newBackfiller(d)

	err := b.StartGap(context.Background(), "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestStartGap_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{execErr: context.Canceled}
	b := newBackfiller(d)

	err := b.StartGap(ctx, "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ── CloseGap ─────────────────────────────────────────────────────────────────

func TestCloseGap_Success(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.CloseGap(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestCloseGap_DBError(t *testing.T) {
	boom := errors.New("db error")
	d := &fakeDB{execErr: boom}
	b := newBackfiller(d)

	err := b.CloseGap(context.Background(), "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestCloseGap_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{execErr: context.Canceled}
	b := newBackfiller(d)

	err := b.CloseGap(ctx, "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ── AdvanceLive ───────────────────────────────────────────────────────────────

func TestAdvanceLive_EmptyPartition_WritesGlobalWatermark(t *testing.T) {
	// Empty partition key represents a global per-connector watermark (no partition).
	// AdvanceLive MUST write to the DB for this case.
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", "", map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestAdvanceLive_EmptyConnectorID_NoOp(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "", `{"p":1}`, map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Empty(t, d.execSQL)
}

func TestAdvanceLive_NilWatermark_NoOp(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", "{}", nil)
	require.NoError(t, err)
	assert.Empty(t, d.execSQL)
}

func TestAdvanceLive_Success(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", `{"p":1}`, map[string]any{"offset": float64(1)})
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestAdvanceLive_DBError(t *testing.T) {
	boom := errors.New("db error")
	d := &fakeDB{execErr: boom}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", "{}", map[string]any{"k": "v"})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestAdvanceLive_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{execErr: context.Canceled}
	b := newBackfiller(d)

	err := b.AdvanceLive(ctx, "conn1", "{}", map[string]any{"k": "v"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ── AdvanceBackfill ───────────────────────────────────────────────────────────

func TestAdvanceBackfill_EmptyPartition_NoOp(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(context.Background(), "conn1", "", map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Empty(t, d.execSQL)
}

func TestAdvanceBackfill_NilWatermark_NoOp(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(context.Background(), "conn1", "{}", nil)
	require.NoError(t, err)
	assert.Empty(t, d.execSQL)
}

func TestAdvanceBackfill_EmptyConnectorID_NoOp(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(context.Background(), "", "{}", map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Empty(t, d.execSQL)
}

func TestAdvanceBackfill_Success(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(context.Background(), "conn1", `{"p":1}`, map[string]any{"offset": float64(2)})
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestAdvanceBackfill_DBError(t *testing.T) {
	boom := errors.New("db error")
	d := &fakeDB{execErr: boom}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(context.Background(), "conn1", "{}", map[string]any{"k": "v"})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestAdvanceBackfill_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{execErr: context.Canceled}
	b := newBackfiller(d)

	err := b.AdvanceBackfill(ctx, "conn1", "{}", map[string]any{"k": "v"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ── boundary inputs ───────────────────────────────────────────────────────────

func TestAllMethods_EmptyConnectorID_NoPanic(t *testing.T) {
	// Empty connectorID is unusual but not special-cased; gRPC handler guarantees
	// non-empty, so the DB layer treats it as any other string.
	d := &fakeDB{row: &fakeRow{scanErr: pgx.ErrNoRows}}
	b := newBackfiller(d)

	_, _, err := b.GetWatermark(context.Background(), "", "{}")
	require.NoError(t, err)

	d.execErr = nil
	require.NoError(t, b.StartGap(context.Background(), "", "{}"))
	require.NoError(t, b.CloseGap(context.Background(), "", "{}"))
	require.NoError(t, b.AdvanceLive(context.Background(), "", "{}", map[string]any{"k": "v"}))
	require.NoError(t, b.AdvanceBackfill(context.Background(), "", "{}", map[string]any{"k": "v"}))
}

func TestAdvanceLive_LargePartitionKey(t *testing.T) {
	// Partition key > 1 KB — no truncation, passes through to Exec.
	key := string(make([]byte, 2048))
	for i := range key {
		key = key[:i] + "a" + key[i+1:]
	}
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", key, map[string]any{"k": "v"})
	require.NoError(t, err)
	require.Len(t, d.execArgs, 1)
	assert.Equal(t, key, d.execArgs[0][1])
}

func TestAdvanceLive_LargeWatermark(t *testing.T) {
	// Watermark ≥ 64 KB.
	large := make(map[string]any)
	for i := 0; i < 1000; i++ {
		large[fmt.Sprintf("key%04d", i)] = string(make([]byte, 100))
	}
	d := &fakeDB{}
	b := newBackfiller(d)

	err := b.AdvanceLive(context.Background(), "conn1", "{}", large)
	require.NoError(t, err)
	assert.Len(t, d.execSQL, 1)
}

func TestGetWatermark_EmptyPartitionStruct(t *testing.T) {
	// partition = "{}" represents the no-partition stream.
	raw, _ := json.Marshal(map[string]any{"offset": float64(1)})
	d := &fakeDB{row: &fakeRow{values: []any{raw}}}
	b := newBackfiller(d)

	wm, found, err := b.GetWatermark(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, wm)
}

// ── concurrency ───────────────────────────────────────────────────────────────

func TestStartGap_Concurrent(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)
	const n = 20

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = b.StartGap(context.Background(), "conn1", "{}")
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
}

func TestAdvanceLive_Concurrent(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)
	const n = 20

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = b.AdvanceLive(context.Background(), "conn1", "{}", map[string]any{"offset": float64(idx)})
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
}

func TestAdvanceBackfill_Concurrent(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)
	const n = 20

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = b.AdvanceBackfill(context.Background(), "conn1", "{}", map[string]any{"offset": float64(idx)})
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
}

func TestCloseGap_Concurrent_WithAdvanceLive(t *testing.T) {
	d := &fakeDB{}
	b := newBackfiller(d)
	const n = 10

	var wg sync.WaitGroup
	errs := make([]error, n*2)
	for i := range n {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = b.CloseGap(context.Background(), "conn1", "{}")
		}(i)
		go func(idx int) {
			defer wg.Done()
			errs[n+idx] = b.AdvanceLive(context.Background(), "conn1", "{}", map[string]any{"offset": float64(idx)})
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
}

// ── IsGapOpen ─────────────────────────────────────────────────────────────────

func TestIsGapOpen_GapOpen(t *testing.T) {
	d := &fakeDB{row: &fakeRow{values: []any{true}}}
	b := newBackfiller(d)

	open, err := b.IsGapOpen(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.True(t, open)
}

func TestIsGapOpen_GapClosed(t *testing.T) {
	d := &fakeDB{row: &fakeRow{values: []any{false}}}
	b := newBackfiller(d)

	open, err := b.IsGapOpen(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.False(t, open)
}

func TestIsGapOpen_NoRow(t *testing.T) {
	d := &fakeDB{row: &fakeRow{scanErr: pgx.ErrNoRows}}
	b := newBackfiller(d)

	open, err := b.IsGapOpen(context.Background(), "conn1", "{}")
	require.NoError(t, err)
	assert.False(t, open)
}

func TestIsGapOpen_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &fakeDB{row: &fakeRow{scanErr: context.Canceled}}
	b := newBackfiller(d)

	_, err := b.IsGapOpen(ctx, "conn1", "{}")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
