package drain

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// worker drains one target_store. Exactly one worker runs per registered store,
// so it owns its store's batching, back-off, and scan without sharing mutable
// state with any other worker.
type worker struct {
	writer  store.StoreWriter
	storeID ingest.StoreID
	outbox  Outbox
	limits  store.BatchLimits
	cfg     Config
	log     *slog.Logger
	backoff *backoff
}

func newWorker(w store.StoreWriter, ob Outbox, cfg Config, log *slog.Logger) *worker {
	limits := w.Limits()
	// A scan must be able to load a whole batch's worth of rows, or the record
	// count trigger (MaxRecords) can never fire and batches would close only on
	// MaxBytes or linger. Raise the page size for this worker so N is reachable.
	if limits.MaxRecords > cfg.PageSize {
		cfg.PageSize = limits.MaxRecords
	}
	return &worker{
		writer:  w,
		storeID: w.StoreID(),
		outbox:  ob,
		limits:  limits,
		cfg:     cfg,
		log:     log.With("component", "drain", "store", string(w.StoreID())),
		backoff: newBackoff(cfg.BackoffInitial, cfg.BackoffMax, cfg.BackoffJitter),
	}
}

// run resumes any pinned batches, then drains pending rows until ctx is done.
func (wk *worker) run(ctx context.Context) {
	wk.resumePinned(ctx)

	var pendingSince time.Time
	for {
		if ctx.Err() != nil {
			return
		}

		rows, err := wk.outbox.ScanPending(ctx, wk.storeID, wk.cfg.PageSize)
		if err != nil {
			if ctx.Err() == nil {
				wk.log.Warn("scan pending failed; backing off", "error", err)
			}
			if !sleepCtx(ctx, wk.cfg.PollInterval) {
				return
			}
			continue
		}
		if len(rows) == 0 {
			pendingSince = time.Time{}
			if !sleepCtx(ctx, wk.cfg.PollInterval) {
				return
			}
			continue
		}

		n, full := nextBatchLen(rows, wk.limits)
		if !full {
			// Not enough rows to close by N or M: hold for the linger bound T,
			// measured from when the partial batch first appeared.
			if pendingSince.IsZero() {
				pendingSince = time.Now()
			}
			if time.Since(pendingSince) < wk.limits.MaxLinger {
				if !sleepCtx(ctx, wk.lingerWait(pendingSince)) {
					return
				}
				continue
			}
		}

		if !wk.flush(ctx, rows[:n]) {
			return
		}
		pendingSince = time.Time{}
	}
}

// lingerWait is how long to sleep while holding a partial batch: the poll
// interval, but never past the remaining linger time.
func (wk *worker) lingerWait(pendingSince time.Time) time.Duration {
	remaining := wk.limits.MaxLinger - time.Since(pendingSince)
	if remaining < wk.cfg.PollInterval {
		if remaining < 0 {
			return 0
		}
		return remaining
	}
	return wk.cfg.PollInterval
}

// flush pins the rows as one batch and delivers it. It returns false only when
// the context was cancelled (the caller should stop).
func (wk *worker) flush(ctx context.Context, rows []PendingRow) bool {
	batchID, err := wk.outbox.PinBatch(ctx, idsOf(rows))
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		wk.log.Warn("pin batch failed; backing off", "error", err)
		return sleepCtx(ctx, wk.cfg.PollInterval)
	}
	return wk.deliver(ctx, toBatch(batchID, rows))
}

// deliver writes one immutable batch, retrying on back-pressure and removing the
// rows only after the store acknowledges. It returns false when the context was
// cancelled mid-flight (the batch is left pinned for the next run).
func (wk *worker) deliver(ctx context.Context, batch store.Batch) bool {
	wk.backoff.reset()
	attempts := 0
	for {
		if ctx.Err() != nil {
			return false
		}

		err := wk.writer.Write(ctx, batch)
		if ctx.Err() != nil {
			// Shutdown raced the write: leave the batch pinned, delete nothing.
			return false
		}
		if err == nil {
			if derr := wk.outbox.DeleteBatch(ctx, batch.ID); derr != nil {
				wk.log.Warn("batch acknowledged but delete failed; will resend",
					"batch_id", batch.ID, "error", derr)
			}
			return true
		}

		var perm *store.PermanentError
		if errors.As(err, &perm) {
			wk.log.Error("permanent store failure; discarding batch",
				"batch_id", batch.ID, "reason", perm.Reason)
			wk.discard(ctx, batch.ID)
			return true
		}

		// Overloaded or any other unexpected error: retry the same batch.
		attempts++
		overloaded := errors.Is(err, store.ErrOverloaded)
		if wk.cfg.MaxRetries > 0 && attempts >= wk.cfg.MaxRetries {
			wk.log.Error("max retries exhausted; discarding batch",
				"batch_id", batch.ID, "attempts", attempts, "overloaded", overloaded, "error", err)
			wk.discard(ctx, batch.ID)
			return true
		}
		wk.log.Warn("store delivery failed; pausing and retrying",
			"batch_id", batch.ID, "attempt", attempts, "overloaded", overloaded, "error", err)
		if !sleepCtx(ctx, wk.backoff.next()) {
			return false
		}
	}
}

// discard removes a batch's rows after a permanent or retry-exhausted failure.
func (wk *worker) discard(ctx context.Context, batchID int64) {
	if err := wk.outbox.DeleteBatch(ctx, batchID); err != nil && ctx.Err() == nil {
		wk.log.Warn("discard delete failed; batch stays pinned", "batch_id", batchID, "error", err)
	}
}

// resumePinned resends every already-pinned batch for this store before any new
// pending row is scanned, so a restart replays the identical batches under their
// original batch_id.
func (wk *worker) resumePinned(ctx context.Context) {
	rows, err := wk.outbox.LoadPinned(ctx, wk.storeID)
	if err != nil {
		if ctx.Err() == nil {
			wk.log.Warn("load pinned failed", "error", err)
		}
		return
	}

	// rows are ordered by batch_id then id; group consecutive rows per batch.
	// LoadPinned only returns pinned rows, so every BatchID is non-nil.
	start := 0
	for i := 1; i <= len(rows); i++ {
		if i == len(rows) || *rows[i].BatchID != *rows[start].BatchID {
			group := rows[start:i]
			if !wk.deliver(ctx, toBatch(*group[0].BatchID, group)) {
				return
			}
			start = i
		}
	}
}
