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
	"strconv"
	"syscall"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/database"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	pool, err := database.Open(ctx, cfg.DSN(), cfg.Database.MaxConns, 0, cfg.Database.ConnMaxLifetime)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	db := store.NewFromPool(pool)

	srv := httpapi.NewServer(db, ":"+strconv.Itoa(cfg.Server.Port))
	srvErrC := make(chan error, 1)
	go func() {
		slog.Info("starting inventory-store", "addr", srv.Addr)
		srvErrC <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-srvErrC:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
	}

	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
