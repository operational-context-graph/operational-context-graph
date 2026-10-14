// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

const defaultContentType = "application/json"

// Beginner is the subset of a pgx pool the Writer needs. *pgxpool.Pool satisfies it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Writer persists batches of normalized records to the PostgreSQL outbox table.
// It satisfies ingest.OutboxWriter.
type Writer struct {
	db    Beginner
	table pgx.Identifier
}

var _ ingest.OutboxWriter = (*Writer)(nil)

// New builds a Writer that inserts into the given table.
func New(db Beginner, table string) *Writer {
	return &Writer{db: db, table: pgx.Identifier{table}}
}

// Write inserts all rows using the PostgreSQL COPY protocol in a single
// transaction. It is atomic: on a nil return every row is durable; on a non-nil
// return the transaction is rolled back and no row is visible. An empty or nil
// slice is a no-op.
func (w *Writer) Write(ctx context.Context, rows []ingest.OutboxRecord) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.CopyFrom(
		ctx,
		w.table,
		[]string{"target_store", "logical_artifact_type", "schema_version", "payload", "content_type"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			ct := rows[i].ContentType
			if ct == "" {
				ct = defaultContentType
			}
			return []any{
				string(rows[i].TargetStore),
				rows[i].LogicalArtifactType,
				rows[i].SchemaVersion,
				rows[i].Payload,
				ct,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("outbox: copy %d rows: %w", len(rows), err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox: commit: %w", err)
	}
	return nil
}
