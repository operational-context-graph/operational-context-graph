// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin/binding"
	"github.com/operational-context-graph/inventory-store/internal/config"
	"github.com/operational-context-graph/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	db, err := store.NewWithConfig(ctx, cfg.DatabaseURL, cfg.DBMaxOpen, cfg.DBMaxIdle, cfg.DBConnMaxLifetime)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	binding.EnableDecoderDisallowUnknownFields = true

	srv := httpapi.NewServer(db, ":"+cfg.HTTPPort)
	go func() {
		slog.Info("starting inventory-store", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}
