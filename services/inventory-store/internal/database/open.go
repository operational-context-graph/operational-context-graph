// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open opens a pgxpool connection pool with the given tuning parameters.
// Zero values for maxOpen/minConns fall back to sensible defaults.
// connMaxLifetime is a duration string (e.g. "5m"); empty defaults to "5m".
func Open(ctx context.Context, databaseURL string, maxOpen, minConns int, connMaxLifetime string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	if maxOpen <= 0 {
		maxOpen = 25
	}
	if minConns <= 0 {
		minConns = 5
	}
	if connMaxLifetime == "" {
		connMaxLifetime = "5m"
	}
	d, err := time.ParseDuration(connMaxLifetime)
	if err != nil {
		return nil, fmt.Errorf("invalid DB_CONN_MAX_LIFETIME %q: %w", connMaxLifetime, err)
	}

	cfg.MaxConns = int32(maxOpen)
	cfg.MinConns = int32(minConns)
	cfg.MaxConnLifetime = d

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
