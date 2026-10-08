// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New opens the connection pool with default tuning and verifies connectivity.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	return NewWithConfig(ctx, databaseURL, 25, 5, "5m")
}

// NewWithConfig opens the connection pool with explicit tuning parameters.
func NewWithConfig(ctx context.Context, databaseURL string, maxOpen, maxIdle int, connMaxLifetime string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	if maxOpen <= 0 {
		maxOpen = 25
	}
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if connMaxLifetime == "" {
		connMaxLifetime = "5m"
	}
	d, err := time.ParseDuration(connMaxLifetime)
	if err != nil {
		d = 5 * time.Minute
	}

	cfg.MaxConns = int32(maxOpen)
	cfg.MinConns = int32(maxIdle)
	cfg.MaxConnLifetime = d

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping checks liveness.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
