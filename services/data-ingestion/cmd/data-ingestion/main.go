// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Command data-ingestion is the entrypoint for the Data Ingestion service.
// It wires configuration and runs the HTTP and gRPC servers.
// Both servers run until the process receives SIGINT or SIGTERM.
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
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/drain"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/httpserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/schemaregistry"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/sourcemapping"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store/inventory"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store/telemetry"
)

func main() {
	if err := run(); err != nil {
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		log.Error("data-ingestion exited with error", "error", err)
		os.Exit(1)
	}
}

// run loads configuration, starts the HTTP and gRPC servers, and blocks until shutdown.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var level slog.Level
	if unmarshalErr := level.UnmarshalText([]byte(cfg.Logging.Level)); unmarshalErr != nil {
		return fmt.Errorf("invalid log level %q: %w", cfg.Logging.Level, unmarshalErr)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseDSN())
	if err != nil {
		return fmt.Errorf("connect outbox database: %w", err)
	}
	defer pool.Close()

	httpSrv := httpserver.New(cfg)

	schemaRegistry := schemaregistry.Stub{}
	mappingEngine := sourcemapping.Stub{}

	pipeline := ingest.NewPipeline(&schemaRegistry, &mappingEngine, outbox.New(pool))

	grpcSrv, err := grpcserver.NewServer(&cfg.GRPC, &cfg.Limits, &cfg.Retry, pipeline, &schemaRegistry)
	if err != nil {
		return err
	}

	// The outbox drain delivers persisted records to the stores.
	storeRegistry := drain.NewStoreRegistry()
	if err := storeRegistry.Register(telemetry.New(telemetry.Config{
		BaseURL:    cfg.TelemetryStore.BaseURL,
		Timeout:    cfg.TelemetryStore.Timeout,
		MaxRecords: cfg.TelemetryStore.MaxRecords,
		MaxBytes:   cfg.TelemetryStore.MaxBytes,
		MaxLinger:  cfg.TelemetryStore.MaxLinger,
	})); err != nil {
		return fmt.Errorf("register telemetry store writer: %w", err)
	}
	if err := storeRegistry.Register(inventory.New(inventory.Config{
		BaseURL:    cfg.InventoryStore.BaseURL,
		Timeout:    cfg.InventoryStore.Timeout,
		MaxRecords: cfg.InventoryStore.MaxRecords,
		MaxBytes:   cfg.InventoryStore.MaxBytes,
		MaxLinger:  cfg.InventoryStore.MaxLinger,
	})); err != nil {
		return fmt.Errorf("register inventory store writer: %w", err)
	}
	drainProc := drain.New(outbox.NewReader(pool), storeRegistry, drain.Config{
		PollInterval:   cfg.Drain.PollInterval,
		BackoffInitial: cfg.Drain.BackoffInitial,
		BackoffMax:     cfg.Drain.BackoffMax,
		BackoffJitter:  cfg.Drain.BackoffJitter,
		MaxRetries:     cfg.Drain.MaxRetries,
		PageSize:       cfg.Drain.PageSize,
	}, log)

	log.Info("data-ingestion starting", "log_level", cfg.Logging.Level)
	log.Info("http server", "host", cfg.Server.Host, "port", cfg.Server.Port)
	log.Info("grpc server", "host", cfg.GRPC.Host, "port", cfg.GRPC.Port, "tls", cfg.GRPC.TLS != nil)

	// The HTTP server, gRPC server, and outbox drain all run until the shared
	// context is cancelled. Any one of them returning triggers shutdown of the
	// others, so a failed component never leaves the process half-running.
	errC := make(chan error, 3)
	go func() { errC <- httpSrv.Run(ctx) }()
	go func() { errC <- grpcSrv.Run(ctx) }()
	go func() { errC <- drainProc.Run(ctx) }()

	// Wait for the first component to stop, then cancel the context so the
	// others exit too, and drain their results.
	first := <-errC
	stop()
	second := <-errC
	third := <-errC

	return firstRealError(first, second, third)
}

// firstRealError returns the first non-nil error that is neither
// http.ErrServerClosed nor context.Canceled, both of which are expected on a
// normal shutdown.
func firstRealError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}
