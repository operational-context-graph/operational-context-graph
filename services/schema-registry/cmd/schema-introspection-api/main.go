// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Command schema-introspection-api serves read-only Schema Registry discovery.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/logging"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("schema introspection API exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := introspection.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Logging)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, cfg.Database.AcquireTimeout)
	pool, err := postgres.Open(startupCtx, cfg.Database)
	cancel()
	if err != nil {
		return err
	}

	store, err := pool.Store(postgres.Timeouts{
		Acquire:           cfg.Database.AcquireTimeout,
		Query:             cfg.Database.QueryTimeout,
		Statement:         cfg.Database.StatementTimeout,
		Lock:              cfg.Database.LockTimeout,
		IdleInTransaction: cfg.Database.IdleInTransactionTimeout,
	})
	if err != nil {
		pool.Close()
		return err
	}
	server := httpapi.NewWithReleaseService(*cfg, pool, release.New(release.NewPostgresReader(store)))
	logger.Info("starting schema introspection API", "configuration", cfg.Redacted())
	if err := server.Run(ctx); err != nil {
		return fmt.Errorf("run HTTP server: %w", err)
	}

	return nil
}
