// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testLimits forms batches quickly: a short linger so partial batches flush.
func testLimits() store.BatchLimits {
	return store.BatchLimits{MaxRecords: 100, MaxBytes: 0, MaxLinger: 5 * time.Millisecond}
}

// runDrain starts a drain in the background and stops it on cleanup.
func runDrain(t *testing.T, ob Outbox, reg *StoreRegistry, cfg Config) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	drain := New(ob, reg, cfg, testLogger())
	done := make(chan struct{})
	go func() {
		_ = drain.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("drain did not stop within timeout")
		}
	})
	return cancel
}

func registryWith(t *testing.T, writers ...store.StoreWriter) *StoreRegistry {
	t.Helper()
	reg := NewStoreRegistry()
	for _, w := range writers {
		require.NoError(t, reg.Register(w))
	}
	return reg
}

func TestWorker_HappyPath_DeliversAndDeletes(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 3, 10)
	w := &fakeWriter{id: "telemetry", limits: testLimits()}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond,
		"all acknowledged rows should be deleted")
	require.GreaterOrEqual(t, w.callCount(), 1)
	assert.Len(t, w.callAt(0).Records, 3)
}

func TestWorker_Overload_RetriesSameBatchThenSucceeds(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{
		id:     "telemetry",
		limits: testLimits(),
		writeFn: func(call int, _ store.Batch) error {
			if call == 0 {
				return store.ErrOverloaded
			}
			return nil
		},
	}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond)
	require.GreaterOrEqual(t, w.callCount(), 2, "batch retried after overload")
	// Identical batch retried: same ID and same ordered records.
	first, second := w.callAt(0), w.callAt(1)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, len(first.Records), len(second.Records))
}

func TestWorker_UnexpectedError_RetriesAndDoesNotDelete(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)
	boom := errors.New("connection reset")
	w := &fakeWriter{
		id:      "telemetry",
		limits:  testLimits(),
		writeFn: func(int, store.Batch) error { return boom },
	}

	runDrain(t, ob, registryWith(t, w), fastConfig()) // MaxRetries 0 => unbounded

	// Rows stay pinned (not deleted) while the writer keeps being retried.
	require.Eventually(t, func() bool { return w.callCount() >= 3 }, time.Second, 2*time.Millisecond)
	assert.Equal(t, 2, ob.total(), "unexpected error must not delete rows")
}

func TestWorker_MaxRetries_Unbounded_NeverDiscards(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{
		id:      "telemetry",
		limits:  testLimits(),
		writeFn: func(int, store.Batch) error { return store.ErrOverloaded },
	}
	cfg := fastConfig()
	cfg.MaxRetries = 0 // no limit for retries

	runDrain(t, ob, registryWith(t, w), cfg)

	require.Eventually(t, func() bool { return w.callCount() >= 5 }, time.Second, 2*time.Millisecond)
	assert.Equal(t, 2, ob.total(), "unbounded retries never discard")
}

func TestWorker_MaxRetries_Bounded_DiscardsAfterN(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{
		id:      "telemetry",
		limits:  testLimits(),
		writeFn: func(int, store.Batch) error { return store.ErrOverloaded },
	}
	cfg := fastConfig()
	cfg.MaxRetries = 3 // retry limit set

	runDrain(t, ob, registryWith(t, w), cfg)

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond,
		"batch discarded after exhausting retries")
	assert.Equal(t, 3, w.callCount(), "exactly MaxRetries attempts before discard")
}

func TestWorker_PermanentError_DiscardsAndContinues(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)
	var once sync.Once
	firstDone := make(chan struct{})
	w := &fakeWriter{
		id:     "telemetry",
		limits: testLimits(),
		writeFn: func(call int, _ store.Batch) error {
			if call == 0 {
				once.Do(func() { close(firstDone) })
				return &store.PermanentError{Reason: "store rejected schema"}
			}
			return nil
		},
	}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	<-firstDone
	// After the permanent failure the first batch is gone; a later-seeded batch
	// still drains, proving the store keeps working.
	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond)
	ob.seed("telemetry", 1, 10)
	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond)
}

func TestWorker_StoreIsolation_OneStalledOneDrains(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10) // stalled store
	ob.seed("inventory", 2, 10) // healthy store

	stalled := &fakeWriter{
		id:      "telemetry",
		limits:  testLimits(),
		writeFn: func(int, store.Batch) error { return store.ErrOverloaded },
	}
	healthy := &fakeWriter{id: "inventory", limits: testLimits()}

	runDrain(t, ob, registryWith(t, stalled, healthy), fastConfig())

	// Healthy store drains despite the stalled one.
	require.Eventually(t, func() bool { return ob.pendingCount("inventory") == 0 }, time.Second, 2*time.Millisecond)
	// Stalled store's rows remain (pinned, never acknowledged): only telemetry's
	// two rows are left in the outbox.
	assert.Equal(t, 2, ob.total())
	assert.Equal(t, 0, ob.pendingCount("telemetry"), "stalled store's rows are pinned, not pending")
}

func TestWorker_ContextCancelMidWrite_LeavesBatchPinned(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 2, 10)

	inWrite := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	w := &fakeWriter{
		id:     "telemetry",
		limits: testLimits(),
		writeFn: func(int, store.Batch) error {
			once.Do(func() { close(inWrite) })
			<-release // block until the test cancels the context
			return nil
		},
	}

	cancel := runDrain(t, ob, registryWith(t, w), fastConfig())

	<-inWrite // worker is inside Write, batch is pinned
	assert.Equal(t, 0, ob.pendingCount("telemetry"), "batch pinned before Write")
	cancel()       // shut down mid-Write
	close(release) // let Write return nil

	// The worker observes cancellation after Write and must not delete: the two
	// rows stay in the outbox (still pinned) for the next run.
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 2, ob.total(), "mid-write cancellation must not delete the batch")
}

func TestWorker_ZeroRegisteredStores_NoOp(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 3, 10)

	runDrain(t, ob, NewStoreRegistry(), fastConfig())

	// No worker exists for any store; rows are untouched.
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 3, ob.total())
}
