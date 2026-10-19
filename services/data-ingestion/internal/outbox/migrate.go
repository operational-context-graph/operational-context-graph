// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Migrate applies the embedded outbox migrations to the database behind pool.
//
// It is idempotent (already-applied migrations are a no-op) and safe to run from
// several replicas at once: the golang-migrate Postgres driver takes an advisory
// lock, so exactly one replica applies the schema while the others wait. A
// migration failure returns a non-nil error and leaves no partial version marked
// current, so the caller can fail startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// OpenDBFromPool shares the pool; closing this *sql.DB does not close it.
	db := stdlib.OpenDBFromPool(pool)

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("outbox: open migration source: %w", err)
	}

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		_ = src.Close()
		_ = db.Close()
		return fmt.Errorf("outbox: build migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = driver.Close()
		_ = src.Close()
		_ = db.Close()
		return fmt.Errorf("outbox: init migrator: %w", err)
	}
	// m.Close() closes both the source and the driver, which releases the
	// advisory lock connection back to the pool.
	defer func() { _, _ = m.Close() }()

	// m.Up has no context parameter; ctx is not forwarded here.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("outbox: apply migrations: %w", err)
	}
	return nil
}
