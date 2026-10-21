// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package outbox implements the persistent PostgreSQL outbox: the concrete
// OutboxWriter the ingest pipeline writes to, and the database schema migrations.
package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migrate applies all pending outbox migrations in dir to the database behind pool.
//
// It is idempotent: already-applied migrations are skipped. Goose serializes
// concurrent callers with a Postgres advisory lock, so running multiple replicas
// simultaneously is safe. A failure returns a non-nil error and leaves the schema
// at the last successfully applied version.
func Migrate(_ context.Context, pool *pgxpool.Pool, dir string) error {
	// OpenDBFromPool shares the pool; closing this *sql.DB does not close it.
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("outbox: set goose dialect: %w", err)
	}

	if err := goose.Up(db, dir); err != nil {
		return fmt.Errorf("outbox: apply migrations: %w", err)
	}
	return nil
}
