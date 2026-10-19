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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpSrv := httpserver.New(cfg)

	grpcSrv, err := grpcserver.NewServer(&cfg.GRPC, &cfg.Limits, &cfg.Retry, &ingest.NoopIngestor{})
	if err != nil {
		return err
	}

	slog.Info("data-ingestion starting", "log_level", cfg.Logging.Level)
	slog.Info("http server", "host", cfg.Server.Host, "port", cfg.Server.Port)
	slog.Info("grpc server", "host", cfg.GRPC.Host, "port", cfg.GRPC.Port, "tls", cfg.GRPC.TLS != nil)

	httpErrC := make(chan error, 1)
	grpcErrC := make(chan error, 1)
	go func() { httpErrC <- httpSrv.Run(ctx) }()
	go func() { grpcErrC <- grpcSrv.Run(ctx) }()

	// Block until either server fails or a signal fires. Then cancel the shared
	// context so the other server shuts down, and collect both results.
	select {
	case <-ctx.Done():
	case err := <-httpErrC:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
			<-grpcErrC
			return err
		}
	case err := <-grpcErrC:
		if err != nil {
			stop()
			<-httpErrC
			return err
		}
	}
	stop()

	httpErr := <-httpErrC
	grpcErr := <-grpcErrC

	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return httpErr
	}
	return grpcErr
}

