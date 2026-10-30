// Package logstore provides a placeholder store writer that acknowledges every batch and logs its batch_id.
package logstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

type Writer struct {
	log *slog.Logger
}

var _ store.StoreWriter = Writer{}

func New(log *slog.Logger) Writer {
	if log == nil {
		log = slog.Default()
	}
	return Writer{log: log}
}

func (w Writer) StoreID() store.StoreID { return "stub" }

// Write acknowledges the batch (returns nil) and logs its identity, so the drain deletes the rows.
func (w Writer) Write(_ context.Context, b store.Batch) error {
	w.log.Info("logstore: delivered batch", "batch_id", b.ID, "records", len(b.Records))
	return nil
}

func (w Writer) Limits() store.BatchLimits {
	return store.BatchLimits{MaxRecords: 100, MaxBytes: 0, MaxLinger: 10 * time.Second}
}
