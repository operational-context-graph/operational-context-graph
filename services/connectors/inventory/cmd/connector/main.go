package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/runner"
)

func main() {
	cfg := loadConfig()

	slog.Info("connector starting",
		"netbox_url", cfg.NetBox.BaseURL,
		"netbox_page_size", cfg.NetBox.PageSize,
		"grpc_endpoint", cfg.GRPC.Endpoint,
		"grpc_connector_id", cfg.GRPC.ConnectorID,
		"grpc_tls", cfg.GRPC.CertFile != "",
		"schema_version", cfg.SchemaVersion,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	conn, err := runner.NewFromConfig(cfg)
	if err != nil {
		slog.Error("failed to initialise connector", "err", err)
		os.Exit(1)
	}

	start := time.Now()

	totals, err := conn.Run(ctx)
	if err != nil {
		slog.Error("connector failed", "err", err)
		os.Exit(1)
	}

	slog.Info("connector finished",
		"published", totals.Published,
		"endpoints_skipped", totals.Skipped,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func loadConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	return cfg
}
