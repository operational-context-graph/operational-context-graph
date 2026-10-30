package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewFromPool creates a Store from an already-opened pool.
func NewFromPool(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// New opens the connection pool with default tuning. Kept for integration tests.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
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

// SchemaReady verifies the schema is present and queryable.
func (s *Store) SchemaReady(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "SELECT 1 FROM assets LIMIT 0")
	return err
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage("{}")
	}
	return b
}

// nullStr converts an empty string to nil so pgx stores NULL instead of "".
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
