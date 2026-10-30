package drain

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Config struct {
	PollInterval   time.Duration
	BackoffInitial time.Duration
	BackoffMax     time.Duration
	// BackoffJitter is the jitter fraction in [0,1] applied to each back-off.
	BackoffJitter float64
	// MaxRetries bounds non-permanent retries for one batch; 0 means unbounded.
	MaxRetries int
	// PageSize bounds the per-scan row count; 0 uses defaultPageSize. Each
	// worker raises its own page size to its store's MaxRecords when larger, so
	// the record-count trigger is always reachable.
	PageSize int
}

// Drain is the top-level delivery process. It starts one worker per registered
// store plus an orphan logger for rows whose store is not registered.
type Drain struct {
	outbox Outbox
	reg    *StoreRegistry
	cfg    Config
	log    *slog.Logger
}

// New builds a Drain. A nil logger uses slog.Default.
func New(ob Outbox, reg *StoreRegistry, cfg Config, log *slog.Logger) *Drain {
	if log == nil {
		log = slog.Default()
	}
	return &Drain{outbox: ob, reg: reg, cfg: cfg, log: log}
}

// Run starts the workers and blocks until ctx is cancelled, then waits for all
// goroutines to finish. With no registered store it still runs the orphan
// logger and otherwise does nothing until cancelled. It returns ctx.Err().
func (d *Drain) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	for _, w := range d.reg.writers {
		worker := newWorker(w, d.outbox, d.cfg, d.log)
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.run(ctx)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		d.logOrphans(ctx)
	}()

	wg.Wait()
	return ctx.Err()
}

// logOrphans periodically warns about pending rows whose target_store has no
// registered writer. Such rows are left pending (never delivered or deleted).
func (d *Drain) logOrphans(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		stores, err := d.outbox.DistinctPendingStores(ctx)
		if err != nil {
			if ctx.Err() == nil {
				d.log.Warn("drain: list pending stores failed", "error", err)
			}
		} else {
			for _, s := range stores {
				if _, ok := d.reg.Writer(s); !ok {
					d.log.Warn("drain: pending rows for unregistered target_store; leaving them pending",
						"target_store", string(s))
				}
			}
		}
		if !sleepCtx(ctx, d.cfg.PollInterval) {
			return
		}
	}
}
