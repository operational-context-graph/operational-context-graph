// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Command data-ingestion is the entrypoint for the Data Ingestion service.
//
// It wires configuration, builds the HTTP server, and runs it until the
// process receives an interrupt or termination signal. Business logic lives
// in the internal packages; this file only bootstraps.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/httpserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
)

func main() {
	if err := run(); err != nil {
		slog.Error("data-ingestion exited with error", "error", err)
		os.Exit(1)
	}
}

// run loads configuration, connects to the outbox database, applies migrations,
// starts the server, and blocks until shutdown.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseDSN())
	if err != nil {
		return fmt.Errorf("connect outbox database: %w", err)
	}
	defer pool.Close()

	if err := outbox.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("apply outbox migrations: %w", err)
	}

	// outbox.Writer is the pipeline's durable sink. The full ingest Pipeline
	// (Schema Registry client #274 + Source Mapping Execution Engine) is not
	// wired yet, so the writer is constructed here and will be injected into the
	// pipeline — and called by the OTLP/gRPC receivers — once those land.
	writer := outbox.New(pool, cfg.Database.OutboxTable)
	_ = writer

	srv := httpserver.New(cfg, pool)

	slog.Info("starting data-ingestion", "host", cfg.Server.Host, "port", cfg.Server.Port)
	if err := srv.Run(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
