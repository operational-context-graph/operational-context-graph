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

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/httpserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
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

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", cfg.Logging.Level, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpSrv := httpserver.New(cfg)

	grpcSrv, err := grpcserver.NewServer(&cfg.GRPC, &cfg.Limits, &cfg.Retry, &ingest.NoopIngestor{}, &grpcserver.NoopSchemaRegistryClient{})
	if err != nil {
		return err
	}

	slog.Info("data-ingestion starting", "log_level", cfg.Logging.Level)
	slog.Info("http server", "host", cfg.Server.Host, "port", cfg.Server.Port)
	slog.Info("grpc server", "host", cfg.GRPC.Host, "port", cfg.GRPC.Port, "tls", cfg.GRPC.TLS != nil)

	errC := make(chan error, 2)
	go func() { errC <- httpSrv.Run(ctx) }()
	go func() { errC <- grpcSrv.Run(ctx) }()

	// Wait for either server to stop. Cancel the context so the other exits too.
	first := <-errC
	stop()
	second := <-errC

	return firstRealError(first, second)
}

// firstRealError returns the first non-nil error that is not http.ErrServerClosed.
func firstRealError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

