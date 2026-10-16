// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// recordingTx is a pgx.Tx test double. It embeds the interface so unused
// methods are present but panic if called, and overrides only what Writer uses.
type recordingTx struct {
	pgx.Tx
	copyFromErr   error
	commitErr     error
	copyFromCalls int
	committed     bool
	rolledBack    bool
}

func (tx *recordingTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	tx.copyFromCalls++
	return 0, tx.copyFromErr
}

func (tx *recordingTx) Commit(context.Context) error {
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *recordingTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

// fakeDB is a DB test double returning a preset transaction.
type fakeDB struct {
	beginErr   error
	tx         *recordingTx
	beginCalls int
}

func (d *fakeDB) Begin(context.Context) (pgx.Tx, error) {
	d.beginCalls++
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	return d.tx, nil
}

func sampleRows(n int) []ingest.OutboxRecord {
	rows := make([]ingest.OutboxRecord, n)
	for i := range rows {
		rows[i] = ingest.OutboxRecord{
			TargetStore:         "telemetry",
			LogicalArtifactType: "ocg.telemetry.log",
			SchemaVersion:       "2.4.0",
			Payload:             []byte(`{"k":"v"}`),
			ContentType:         "application/json",
		}
	}
	return rows
}

func TestWriter_Write_BatchCommitted(t *testing.T) {
	db := &fakeDB{tx: &recordingTx{}}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(3))

	require.NoError(t, err)
	assert.Equal(t, 1, db.tx.copyFromCalls)
	assert.True(t, db.tx.committed)
}

func TestWriter_Write_SingleRowCommitted(t *testing.T) {
	db := &fakeDB{tx: &recordingTx{}}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(1))

	require.NoError(t, err)
	assert.True(t, db.tx.committed)
}

func TestWriter_Write_CopyFailureRollsBack(t *testing.T) {
	boom := errors.New("constraint violation")
	db := &fakeDB{tx: &recordingTx{copyFromErr: boom}}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(2))

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.False(t, db.tx.committed)
	assert.True(t, db.tx.rolledBack)
}

func TestWriter_Write_BeginFailure(t *testing.T) {
	boom := errors.New("pool exhausted")
	db := &fakeDB{beginErr: boom}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(1))

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestWriter_Write_CommitFailureRollsBack(t *testing.T) {
	boom := errors.New("commit failed")
	db := &fakeDB{tx: &recordingTx{commitErr: boom}}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(1))

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.True(t, db.tx.rolledBack)
}

func TestWriter_Write_ContextCancellationRollsBack(t *testing.T) {
	db := &fakeDB{tx: &recordingTx{copyFromErr: context.Canceled}}
	w := New(db)

	err := w.Write(context.Background(), sampleRows(1))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, db.tx.rolledBack)
}

func TestWriter_Write_EmptySliceIsNoOp(t *testing.T) {
	db := &fakeDB{tx: &recordingTx{}}
	w := New(db)

	require.NoError(t, w.Write(context.Background(), []ingest.OutboxRecord{}))
	assert.Equal(t, 0, db.beginCalls)
}

func TestWriter_Write_NilSliceIsNoOp(t *testing.T) {
	db := &fakeDB{tx: &recordingTx{}}
	w := New(db)

	require.NoError(t, w.Write(context.Background(), nil))
	assert.Equal(t, 0, db.beginCalls)
}

func TestWriter_Write_Concurrent(t *testing.T) {
	const goroutines = 6
	var wg sync.WaitGroup
	dbs := make([]*fakeDB, goroutines)
	errs := make([]error, goroutines)

	for g := 0; g < goroutines; g++ {
		dbs[g] = &fakeDB{tx: &recordingTx{}}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := New(dbs[idx])
			errs[idx] = w.Write(context.Background(), sampleRows(2))
		}(g)
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		require.NoError(t, errs[g])
		assert.True(t, dbs[g].tx.committed)
	}
}
