// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
)

// The fakes below inject failures through the outbox.Querier seam so the
// Reader's row-iteration error branches can be exercised without a database.

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// fakeRows embeds pgx.Rows so only the methods the Reader uses are overridden;
// any other call panics, which keeps the fake honest.
type fakeRows struct {
	pgx.Rows
	rowsToYield int
	yielded     int
	scanErr     error
	errErr      error
}

func (r *fakeRows) Next() bool {
	if r.yielded >= r.rowsToYield {
		return false
	}
	r.yielded++
	return true
}
func (r *fakeRows) Scan(...any) error { return r.scanErr }
func (r *fakeRows) Err() error        { return r.errErr }
func (r *fakeRows) Close()            {}

type fakeQuerier struct {
	rows       *fakeRows
	queryErr   error
	rowScanErr error
}

func (q *fakeQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	return q.rows, nil
}

func (q *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return errRow{err: q.rowScanErr}
}

func (q *fakeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func TestReader_ScanPending_RowScanError(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 1, scanErr: errors.New("bad scan")}})
	_, err := r.ScanPending(context.Background(), "telemetry", 10)
	require.Error(t, err)
}

func TestReader_ScanPending_RowsErr(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 0, errErr: errors.New("rows err")}})
	_, err := r.ScanPending(context.Background(), "telemetry", 10)
	require.Error(t, err)
}

func TestReader_PinBatch_ReserveSeqError(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rowScanErr: errors.New("nextval failed")})
	_, err := r.PinBatch(context.Background(), []int64{1, 2})
	require.Error(t, err)
}

func TestReader_LoadPinned_RowScanError(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 1, scanErr: errors.New("bad scan")}})
	_, err := r.LoadPinned(context.Background(), "telemetry")
	require.Error(t, err)
}

func TestReader_LoadPinned_RowsErr(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 0, errErr: errors.New("rows err")}})
	_, err := r.LoadPinned(context.Background(), "telemetry")
	require.Error(t, err)
}

func TestReader_DistinctPendingStores_RowScanError(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 1, scanErr: errors.New("bad scan")}})
	_, err := r.DistinctPendingStores(context.Background())
	require.Error(t, err)
}

func TestReader_DistinctPendingStores_RowsErr(t *testing.T) {
	r := outbox.NewReader(&fakeQuerier{rows: &fakeRows{rowsToYield: 0, errErr: errors.New("rows err")}})
	_, err := r.DistinctPendingStores(context.Background())
	require.Error(t, err)
}
