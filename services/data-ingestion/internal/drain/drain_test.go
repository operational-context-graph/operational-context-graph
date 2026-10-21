// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

func TestWorker_PinFailure_BacksOffWithoutDelivering(t *testing.T) {
	ob := &fakeOutbox{pinErr: errors.New("db down")}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{id: "telemetry", limits: testLimits()}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, 0, w.callCount(), "pin failure must not reach the store")
	assert.Equal(t, 2, ob.total(), "rows remain pending when pin fails")
}

func TestWorker_ScanError_BacksOff(t *testing.T) {
	ob := &fakeOutbox{scanErr: errors.New("db down")}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{id: "telemetry", limits: testLimits()}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, 0, w.callCount())
	assert.Equal(t, 2, ob.total())
}

func TestWorker_ResumePinnedLoadError_StillDrainsPending(t *testing.T) {
	ob := &fakeOutbox{loadErr: errors.New("load pinned failed")}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{id: "telemetry", limits: testLimits()}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond,
		"a load-pinned error must not stop normal draining")
}

func TestWorker_AckThenDeleteFails_LeavesRows(t *testing.T) {
	ob := &fakeOutbox{deleteErr: errors.New("delete failed")}
	ob.seed("telemetry", 2, 10)
	w := &fakeWriter{id: "telemetry", limits: testLimits()} // acks

	runDrain(t, ob, registryWith(t, w), fastConfig())

	// Writer acked but the delete failed: the rows stay pinned (not pending) and
	// will be resent on the next restart. The running loop does not rescan a
	// pinned batch, so the store is called exactly once.
	require.Eventually(t, func() bool { return w.callCount() >= 1 }, time.Second, 2*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 2, ob.total(), "rows remain because delete failed")
	assert.Equal(t, 0, ob.pendingCount("telemetry"), "rows stay pinned, not pending")
}

func TestDrain_LogOrphansError_KeepsRunning(t *testing.T) {
	ob := &fakeOutbox{distinctErr: errors.New("query failed")}
	cap := &capHandler{}

	runDrainWithLogger(t, ob, NewStoreRegistry(), fastConfig(), slog.New(cap))

	require.Eventually(t, func() bool { return cap.contains("list pending stores failed") }, time.Second, 2*time.Millisecond)
}

func TestDrain_New_NilLoggerAndDefaultPageSize(t *testing.T) {
	ob := &fakeOutbox{}
	// Config{} has a zero PageSize; New must fill the default and accept a nil
	// logger.
	d := New(ob, NewStoreRegistry(), Config{PollInterval: 2 * time.Millisecond, BackoffInitial: time.Millisecond, BackoffMax: 2 * time.Millisecond}, nil)
	require.NotNil(t, d)
	assert.Equal(t, defaultPageSize, d.cfg.PageSize)
	assert.NotNil(t, d.log)
}

func TestWorker_SingleRow_FlushesOnLinger(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 1, 10) // one row, far below MaxRecords
	var delivered store.Batch
	w := &fakeWriter{
		id:     "telemetry",
		limits: store.BatchLimits{MaxRecords: 100, MaxBytes: 0, MaxLinger: 5 * time.Millisecond},
		writeFn: func(_ int, b store.Batch) error {
			delivered = b
			return nil
		},
	}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond)
	assert.Len(t, delivered.Records, 1, "single row ships as a batch of one after linger")
}

func TestWorker_OversizedRow_ShipsAsBatchOfOne(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("telemetry", 1, 1000) // payload far larger than MaxBytes
	w := &fakeWriter{
		id:     "telemetry",
		limits: store.BatchLimits{MaxRecords: 100, MaxBytes: 10, MaxLinger: 5 * time.Millisecond},
	}

	runDrain(t, ob, registryWith(t, w), fastConfig())

	require.Eventually(t, func() bool { return ob.total() == 0 }, time.Second, 2*time.Millisecond)
	assert.Len(t, w.callAt(0).Records, 1)
}

func TestWorker_PageSizeRaisedToMaxRecords_FlushesOnCount(t *testing.T) {
	ob := &fakeOutbox{}
	// MaxRecords exceeds the configured PageSize; the worker must still fill a
	// batch by count rather than waiting for the linger timer.
	ob.seed("telemetry", 6, 10)
	w := &fakeWriter{
		id:     "telemetry",
		limits: store.BatchLimits{MaxRecords: 5, MaxBytes: 0, MaxLinger: time.Hour}, // linger effectively disabled
	}
	cfg := fastConfig()
	cfg.PageSize = 2 // smaller than MaxRecords

	runDrain(t, ob, registryWith(t, w), cfg)

	// With a 1h linger, a batch can only flush if N (5) is reached, which
	// requires the page size to have been raised to >= 5.
	require.Eventually(t, func() bool { return w.callCount() >= 1 }, time.Second, 2*time.Millisecond)
	assert.Len(t, w.callAt(0).Records, 5, "batch closes on MaxRecords despite small configured PageSize")
}
