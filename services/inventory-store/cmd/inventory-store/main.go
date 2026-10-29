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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/operational-context-graph/inventory-store/internal/config"
	"github.com/operational-context-graph/inventory-store/internal/database"
	"github.com/operational-context-graph/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/inventory-store/internal/inventory/adapters"
	"github.com/operational-context-graph/inventory-store/internal/store"
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

	// Asset read-path switch (additive). OCG_IS_READ_PATH = dao (default) | repo | compare.
	// When set, wrap the store so asset reads route through the new repository; the wrapper
	// embeds *store.Store, so all other behaviour is unchanged. The result still satisfies the
	// interface NewServer expects, so no existing code changes.
	readPath := adapters.ParseReadPath(os.Getenv("OCG_IS_READ_PATH"))
	srv := httpapi.NewServer(newRepositoryReader(db, pool, readPath), ":"+strconv.Itoa(cfg.Server.Port))
	slog.Info("asset read path configured", "path", string(readPath))
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

// newRepositoryReader builds the asset-read handler for the given path. It always returns a
// StoreReader that embeds the legacy store; in the default "dao" path it passes every call
// straight through, so behaviour is identical to passing the bare store. For "repo"/"compare"
// it routes asset reads to the new PostgresAssetRepository. Returned value satisfies the
// interface httpapi.NewServer expects.
func newRepositoryReader(db *store.Store, pool *pgxpool.Pool, path adapters.ReadPath) *adapters.StoreReader {
	return adapters.NewStoreReader(db, adapters.NewPostgresAssetRepository(pool), path)
}
