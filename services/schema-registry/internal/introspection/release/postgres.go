package release

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// PostgresReader adapts the shared PostgreSQL read boundary to release use cases.
type PostgresReader struct{ store *postgres.Store }

func NewPostgresReader(store *postgres.Store) *PostgresReader { return &PostgresReader{store: store} }

func (r *PostgresReader) Read(ctx context.Context, fn func(context.Context, Snapshot) error) error {
	return r.store.Read(ctx, func(readCtx context.Context, snapshot *postgres.Snapshot) error { return fn(readCtx, snapshot) })
}
