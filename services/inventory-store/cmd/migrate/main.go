// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Command migrate applies pending schema migrations to the inventory-store PostgreSQL database.
// It runs as a Kubernetes Job before the inventory-store pods start.
// Service pods never apply migrations.
//
// Required environment variables:
//
//	DATABASE_URL   PostgreSQL connection URL, e.g. postgres://user:pass@host:5432/inventory
//
// The migrator exits 0 on success and non-zero on failure.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/operational-context-graph/inventory-store/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied successfully")
}

func run() error {
	dsn := requireEnv("DATABASE_URL")

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
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

func requireEnv(key string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		slog.Error("required environment variable not set", "variable", key)
		os.Exit(1)
	}
	return v
}
