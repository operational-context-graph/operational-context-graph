// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package postgres owns read-only PostgreSQL pool setup for Schema Registry.
package postgres

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

const runtimeRole = "schema_registry_api"

// ReadinessChecker performs the bounded query used by the readiness probe.
type ReadinessChecker interface {
	Ready(context.Context) error
	Close()
}

// Pool is the runtime-owned PostgreSQL pool.
type Pool struct {
	pool *pgxpool.Pool
}

// Store creates the request-scoped read boundary over this runtime pool.
func (p *Pool) Store(timeouts Timeouts) (*Store, error) { return NewStore(p.pool, timeouts) }

// Open constructs and verifies a pool before the HTTP listener starts.
func Open(ctx context.Context, cfg introspection.DatabaseConfig) (*Pool, error) {
	poolConfig, err := configurePool(cfg.URL, cfg.PoolMinConnections, cfg.PoolMaxConnections)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	result := &Pool{pool: pool}
	if err := result.Ready(ctx); err != nil {
		result.Close()
		return nil, fmt.Errorf("verify database connection: %w", err)
	}
	return result, nil
}

func configurePool(databaseURL string, minConnections, maxConnections int32) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MinConns = minConnections
	poolConfig.MaxConns = maxConnections
	poolConfig.ConnConfig.RuntimeParams["search_path"] = "schema_registry,pg_catalog"
	if poolConfig.ConnConfig.TLSConfig == nil {
		return nil, fmt.Errorf("database TLS verification configuration is required")
	}
	poolConfig.ConnConfig.TLSConfig.MinVersion = tls.VersionTLS13
	poolConfig.PrepareConn = func(ctx context.Context, conn *pgx.Conn) (bool, error) {
		// The externally provisioned login must be a member of this NOLOGIN role.
		if _, err := conn.Exec(ctx, "SET ROLE "+runtimeRole); err != nil {
			return false, fmt.Errorf("assume read-only database role: %w", err)
		}
		return true, nil
	}
	return poolConfig, nil
}

// Ready runs the minimal bounded probe query. It never inspects release state.
func (p *Pool) Ready(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire readiness connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		return fmt.Errorf("execute readiness query: %w", err)
	}
	return nil
}

// Close releases all pool resources.
func (p *Pool) Close() {
	p.pool.Close()
}
