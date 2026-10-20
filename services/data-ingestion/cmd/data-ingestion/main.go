// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Command data-ingestion is the entrypoint for the Data Ingestion service.
//
// It wires configuration, builds the HTTP server and gRPC server, and runs
// both until the process receives an interrupt or termination signal.
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
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store/logstore"
)

func main() {
	if err := run(); err != nil {
		slog.Error("data-ingestion exited with error", "error", err)
		os.Exit(1)
	}
}

// run loads configuration, starts the HTTP and gRPC servers, and blocks until shutdown.
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

	httpSrv := httpserver.New(cfg)

	schemaRegistry := schemaregistry.Stub{}
	mappingEngine := sourcemapping.Stub{}

	pipeline := ingest.NewPipeline(&schemaRegistry, &mappingEngine, outbox.New(pool))

	grpcSrv, err := grpcserver.NewServer(&cfg.GRPC, &cfg.Limits, &cfg.Retry, pipeline)
	if err != nil {
		return err
	}

	// The outbox drain delivers persisted records to the stores. The
	// LogStoreWriter is a placeholder sink that logs each batch; real store
	// writers replace it later.
	storeRegistry := drain.NewStoreRegistry()
	if err := storeRegistry.Register(logstore.New(slog.Default())); err != nil {
		return fmt.Errorf("register store writer: %w", err)
	}
	drainProc := drain.New(outbox.NewReader(pool), storeRegistry, drain.Config{
		PollInterval:   cfg.Drain.PollInterval,
		BackoffInitial: cfg.Drain.BackoffInitial,
		BackoffMax:     cfg.Drain.BackoffMax,
		BackoffJitter:  cfg.Drain.BackoffJitter,
		MaxRetries:     cfg.Drain.MaxRetries,
	}, slog.Default())

	slog.Info("data-ingestion starting", "log_level", cfg.Logging.Level)
	slog.Info("http server", "host", cfg.Server.Host, "port", cfg.Server.Port)
	slog.Info("grpc server", "host", cfg.GRPC.Host, "port", cfg.GRPC.Port, "tls", cfg.GRPC.TLS != nil)

	grpcErrC := make(chan error, 1)
	go func() {
		grpcErrC <- grpcSrv.Run(ctx)
	}()

	// The drain runs until the shared context is cancelled on shutdown.
	drainErrC := make(chan error, 1)
	go func() {
		drainErrC <- drainProc.Run(ctx)
	}()

	httpErr := httpSrv.Run(ctx)
	// Cancel the shared context so the gRPC goroutine exits when the HTTP server
	// stops for any reason. Without this, an HTTP bind error would leave the
	// process blocked on <-grpcErrC forever.
	// The reverse is also true: if grpcSrv.Run returns (e.g. bind error), grpcErrC
	// closes, <-grpcErrC unblocks, and main returns — taking the HTTP server down.
	stop()

	grpcErr := <-grpcErrC
	// The drain returns context.Canceled on a normal shutdown; that is not a failure.
	if drainErr := <-drainErrC; drainErr != nil && !errors.Is(drainErr, context.Canceled) {
		return drainErr
	}

	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return httpErr
	}
	return grpcErr
}
