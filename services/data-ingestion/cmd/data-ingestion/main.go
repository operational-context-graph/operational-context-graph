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
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/httpserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/schemaregistry"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/sourcemapping"
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

	if err := outbox.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("apply outbox migrations: %w", err)
	}

	// outbox.Writer is the pipeline's durable sink. The full ingest Pipeline
	// (Schema Registry client #274 + Source Mapping Execution Engine) is not
	// wired yet, so the writer is constructed here and will be injected into the
	// pipeline — and called by the OTLP/gRPC receivers — once those land.
	writer := outbox.New(pool, cfg.Database.OutboxTable)
	_ = writer

	httpSrv := httpserver.New(cfg)

	schemaRegistry := schemaregistry.Stub{}
	mappingEngine := sourcemapping.Stub{}

	pipeline := ingest.NewPipeline(&schemaRegistry, &mappingEngine, writer)

	grpcSrv, err := grpcserver.NewServer(&cfg.GRPC, &cfg.Limits, &cfg.Retry, pipeline)
	if err != nil {
		return err
	}

	slog.Info("data-ingestion starting", "log_level", cfg.Logging.Level)
	slog.Info("http server", "host", cfg.Server.Host, "port", cfg.Server.Port)
	slog.Info("grpc server", "host", cfg.GRPC.Host, "port", cfg.GRPC.Port, "tls", cfg.GRPC.TLS != nil)

	grpcErrC := make(chan error, 1)
	go func() {
		grpcErrC <- grpcSrv.Run(ctx)
	}()

	httpErr := httpSrv.Run(ctx)
	// Cancel the shared context so the gRPC goroutine exits when the HTTP server
	// stops for any reason. Without this, an HTTP bind error would leave the
	// process blocked on <-grpcErrC forever.
	// The reverse is also true: if grpcSrv.Run returns (e.g. bind error), grpcErrC
	// closes, <-grpcErrC unblocks, and main returns — taking the HTTP server down.
	stop()

	grpcErr := <-grpcErrC

	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return httpErr
	}
	return grpcErr
}
