// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

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
	"time"

	"github.com/gin-gonic/gin/binding"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/database"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/dorissql"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/store"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/streamload"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(ctx); err != nil {
		log.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var level slog.Level
	if unmarshalErr := level.UnmarshalText([]byte(cfg.LogLevel)); unmarshalErr != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	dorissql.RegisterTimeArgs()

	binding.EnableDecoderDisallowUnknownFields = true

	sqlDB, err := database.Open(ctx, cfg.DorisDSN)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }() // Close errors are non-actionable on shutdown.

	db, err := store.NewFromDB(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("prepare store: %w", err)
	}
	defer func() { _ = db.Close() }() // Close errors are non-actionable on shutdown.

	sl := streamload.NewClient(cfg.DorisStreamLoadURL, cfg.DorisStreamLoadUser, cfg.DorisStreamLoadPassword)

	srv := httpapi.NewServer(db, sl, ":"+cfg.HTTPPort, cfg.MaxIngestBodyBytes)
	srvErrC := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "starting telemetry-store", "addr", srv.Addr)
		srvErrC <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case srvErr := <-srvErrC:
		if srvErr != nil && !errors.Is(srvErr, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", srvErr)
		}
	}
	log.InfoContext(ctx, "shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutErr := srv.Shutdown(shutCtx); shutErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutErr)
	}
	return nil
}
