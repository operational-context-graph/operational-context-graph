// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Command migrate applies pending schema migrations to the Doris ocg database.
// It runs as a Kubernetes Job before the telemetry-store pods start, per AO-008.
// Service pods never apply migrations.
//
// Required environment variables:
//
//	DORIS_DSN              MySQL DSN for the Doris FE, e.g. root:@tcp(doris-fe:9030)/
//	REPLICATION_NUM        Doris replication_num; default "3", set "1" on a single-BE dev cluster
//	BUCKETS                Doris bucket count; default "10"
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

	_ "github.com/go-mysql-org/go-mysql/driver"
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
	dsn := requireEnv("DORIS_DSN")
	replNum := envOr("REPLICATION_NUM", "3")
	buckets := envOr("BUCKETS", "10")

	// Inject Doris DDL substitution variables before provider creation so
	// goose.WithEnvInterpolation can substitute ${REPLICATION_NUM} and ${BUCKETS}.
	os.Setenv("REPLICATION_NUM", replNum)
	os.Setenv("BUCKETS", buckets)

	dorissql.RegisterTimeArgs()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	// Bootstrap: goose tries to create its version table before running any migration.
	// The ocg database does not exist on a fresh cluster, so CREATE TABLE ocg.goose_db_version
	// would fail. Create the database explicitly first, then let migration 001 be idempotent.
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

func requireEnv(key string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		slog.Error("required environment variable not set", "variable", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
