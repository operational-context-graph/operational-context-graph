// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Command migrate applies pending schema migrations to the Doris ocg database.
// The migrator exits 0 on success and non-zero on failure.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/go-mysql-org/go-mysql/driver"
	"github.com/operational-context-graph/telemetry-store/internal/config"
	"github.com/operational-context-graph/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/telemetry-store/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied successfully")
}

func run() error {
	cfg, err := config.LoadMigrator()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Expose DDL substitution variables so expandEnvWithDefaults can resolve
	// ${REPLICATION_NUM:-3} and ${BUCKETS:-10} in migration SQL files.
	os.Setenv("REPLICATION_NUM", cfg.DorisReplicationNum)
	os.Setenv("BUCKETS", cfg.DorisBuckets)

	dorissql.RegisterTimeArgs()

	db, err := sql.Open("mysql", cfg.DorisDSN)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS ocg"); err != nil {
		return fmt.Errorf("bootstrap ocg database: %w", err)
	}

	provider, err := migrations.NewProvider(db)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	results, err := provider.Up(context.Background())
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	for _, r := range results {
		if r.Error != nil {
			return fmt.Errorf("migration %s failed: %w", r.Source.Path, r.Error)
		}
		if !r.Empty {
			slog.Info("applied migration",
				"version", r.Source.Version,
				"path", r.Source.Path,
				"duration_ms", r.Duration.Milliseconds(),
			)
		}
	}
	return nil
}
