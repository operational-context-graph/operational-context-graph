// Command migrate applies or rolls back Goose migrations for Apache Doris.
//
// It uses the Goose Provider API with a custom Doris dialect so that no
// MySQL driver other than go-mysql-org/go-mysql is linked. The stock goose
// CLI cannot be used against Doris because its built-in MySQL support uses
// a different driver that conflicts at registration time.
//
// Usage:
//
//	migrate          — apply all pending migrations (default, used in production)
//	migrate -down    — roll back the most recently applied migration (dev only)
//
// Configuration is read from the same environment variables as the service
// itself; see internal/config/config.go for the full list. The migration
// files are read from /migrations inside the container image.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-mysql-org/go-mysql/driver"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorismigrate"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorissql"
)

func main() {
	down := flag.Bool("down", false, "roll back the most recently applied migration (dev only)")
	flag.Parse()

	if err := run(*down); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run(down bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dorissql.RegisterTimeArgs()

	db, err := sql.Open("mysql", cfg.DorisDSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Doris accepts TCP connections before its query engine is ready to serve SQL.
	// Ping can fail even after sql.Open succeeds, so we retry until the server is queryable.
	if waitErr := waitForDoris(db); waitErr != nil {
		return waitErr
	}

	dbName := os.Getenv("DORIS_DATABASE")
	if dbName == "" {
		return fmt.Errorf("DORIS_DATABASE environment variable is required")
	}

	// Create the database before Goose initialises its version tracking table,
	// which itself lives in the target database. The DSN intentionally has no
	// database component so this connection can run DDL at the server level.
	if _, dbErr := db.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS "+dbName); dbErr != nil {
		return fmt.Errorf("create database: %w", dbErr)
	}

	store, err := database.NewStoreFromQuerier(dbName+".goose_db_version_telemetry_store", &dorismigrate.Querier{})
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectCustom, db, os.DirFS("/migrations"),
		goose.WithStore(store),
	)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	if down {
		result, downErr := provider.Down(context.Background())
		if downErr != nil {
			return fmt.Errorf("roll back migration: %w", downErr)
		}
		if result != nil {
			log.Printf("migration %d rolled back (%s)", result.Source.Version, result.Duration)
		} else {
			log.Println("no migrations to roll back")
		}
		return nil
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

// waitForDoris polls until Doris accepts connections. sql.Open only validates
// the DSN without opening a real connection, so the Doris MySQL-compatible
// listener may still be initialising when this binary starts. Repeated pings
// are required before any DDL statement can succeed.
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
