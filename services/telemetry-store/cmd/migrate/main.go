// Command migrate applies pending Goose migrations to Apache Doris.
//
// It uses the Goose Provider API with a custom Doris dialect so that no
// MySQL driver other than go-mysql-org/go-mysql is linked. The stock goose
// CLI cannot be used against Doris because its built-in MySQL support uses
// a different driver that conflicts at registration time.
//
// Required environment variables:
//
//	DORIS_MIGRATE_DSN        — Doris DSN without a database, e.g. "root:pass@tcp(host:9030)/"
//	DORIS_REPLICATION_NUM    — replication_num for the tracking table, e.g. "1" or "3"
//	DORIS_BUCKETS            — bucket count used in migration SQL, e.g. "10"
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorismigrate"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run() error {
	dsn := requireEnv("DORIS_MIGRATE_DSN")

	dorissql.RegisterTimeArgs()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := waitForDoris(db); err != nil {
		return err
	}

	if _, err := db.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS ocg"); err != nil {
		return fmt.Errorf("create database: %w", err)
	}

	store, err := database.NewStoreFromQuerier("ocg.goose_db_version_telemetry_store", &dorismigrate.Querier{})
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectCustom, db, migrations.FS,
		goose.WithStore(store),
	)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	results, err := provider.Up(context.Background())
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	for _, r := range results {
		log.Printf("migration %d applied (%s)", r.Source.Version, r.Duration)
	}
	if len(results) == 0 {
		log.Println("no pending migrations")
	}
	return nil
}

func requireEnv(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		log.Fatalf("missing required environment variable: %s", key)
	}
	return v
}

func waitForDoris(db *sql.DB) error {
	const maxAttempts = 30
	const delay = 5 * time.Second
	for i := range maxAttempts {
		err := db.PingContext(context.Background())
		if err == nil {
			return nil
		}
		log.Printf("doris not ready (attempt %d/%d): %v", i+1, maxAttempts, err)
		time.Sleep(delay)
	}
	return fmt.Errorf("doris not ready after %d attempts", maxAttempts)
}
