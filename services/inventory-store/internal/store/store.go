// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Asset mirrors the assets table.
type Asset struct {
	ID          uuid.UUID       `json:"id"`
	AssetType   string          `json:"asset_type"`
	Source      string          `json:"source"`
	ExternalID  string          `json:"external_id"`
	Name        *string         `json:"name,omitempty"`
	IPAddresses json.RawMessage `json:"ip_addresses,omitempty"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
	IngestedAt  time.Time       `json:"ingested_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// AssetInput is the request body for POST /assets.
type AssetInput struct {
	AssetType   string          `json:"asset_type"`
	Source      string          `json:"source"`
	ExternalID  string          `json:"external_id"`
	Name        *string         `json:"name,omitempty"`
	IPAddresses json.RawMessage `json:"ip_addresses,omitempty"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
}

// Relationship mirrors the relationships table.
type Relationship struct {
	ID               uuid.UUID       `json:"id"`
	SourceAssetID    uuid.UUID       `json:"source_asset_id"`
	TargetAssetID    uuid.UUID       `json:"target_asset_id"`
	RelationshipType string          `json:"relationship_type"`
	Source           string          `json:"source"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
	IngestedAt       time.Time       `json:"ingested_at"`
}

// RelationshipInput is the request body for POST /relationships.
type RelationshipInput struct {
	SourceAssetID    uuid.UUID       `json:"source_asset_id"`
	TargetAssetID    uuid.UUID       `json:"target_asset_id"`
	RelationshipType string          `json:"relationship_type"`
	Source           string          `json:"source"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
}

// Cursor encodes the keyset pagination position.
type Cursor struct {
	IngestedAt time.Time `json:"t"`
	ID         uuid.UUID `json:"i"`
}

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	return c, json.Unmarshal(b, &c)
}

// New opens the connection pool and verifies connectivity.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	maxOpen, _ := strconv.Atoi(os.Getenv("DB_MAX_OPEN_CONNS"))
	if maxOpen <= 0 {
		maxOpen = 25
	}
	maxIdle, _ := strconv.Atoi(os.Getenv("DB_MAX_IDLE_CONNS"))
	if maxIdle <= 0 {
		maxIdle = 5
	}
	lifetime := os.Getenv("DB_CONN_MAX_LIFETIME")
	if lifetime == "" {
		lifetime = "5m"
	}
	d, err := time.ParseDuration(lifetime)
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

// UpsertAsset inserts or updates an asset. Returns (asset, created, error).
func (s *Store) UpsertAsset(ctx context.Context, in AssetInput) (Asset, bool, error) {
	const q = `
INSERT INTO assets (asset_type, source, external_id, name, ip_addresses, attributes)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, external_id) DO UPDATE SET
    asset_type   = EXCLUDED.asset_type,
    name         = EXCLUDED.name,
    ip_addresses = EXCLUDED.ip_addresses,
    attributes   = EXCLUDED.attributes,
    updated_at   = NOW()
RETURNING id, asset_type, source, external_id, name, ip_addresses, attributes, ingested_at, updated_at,
          (xmax = 0) AS inserted`

	row := s.pool.QueryRow(ctx, q,
		in.AssetType, in.Source, in.ExternalID, in.Name,
		nullJSON(in.IPAddresses), nullJSON(in.Attributes),
	)

	var a Asset
	var inserted bool
	err := row.Scan(
		&a.ID, &a.AssetType, &a.Source, &a.ExternalID, &a.Name,
		&a.IPAddresses, &a.Attributes, &a.IngestedAt, &a.UpdatedAt,
		&inserted,
	)
	if err != nil {
		return Asset{}, false, fmt.Errorf("upsert asset: %w", err)
	}
	return a, inserted, nil
}

// GetAsset fetches an asset by internal UUID.
func (s *Store) GetAsset(ctx context.Context, id uuid.UUID) (Asset, error) {
	const q = `SELECT id, asset_type, source, external_id, name, ip_addresses, attributes, ingested_at, updated_at
               FROM assets WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	var a Asset
	err := row.Scan(&a.ID, &a.AssetType, &a.Source, &a.ExternalID, &a.Name,
		&a.IPAddresses, &a.Attributes, &a.IngestedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

// GetAssetByExternal fetches an asset by (source, external_id).
func (s *Store) GetAssetByExternal(ctx context.Context, source, externalID string) (Asset, error) {
	const q = `SELECT id, asset_type, source, external_id, name, ip_addresses, attributes, ingested_at, updated_at
               FROM assets WHERE source = $1 AND external_id = $2`
	row := s.pool.QueryRow(ctx, q, source, externalID)
	var a Asset
	err := row.Scan(&a.ID, &a.AssetType, &a.Source, &a.ExternalID, &a.Name,
		&a.IPAddresses, &a.Attributes, &a.IngestedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset by external: %w", err)
	}
	return a, nil
}

// ListAssets returns a page of assets filtered by type or source.
func (s *Store) ListAssets(ctx context.Context, assetType, source string, limit int, after *Cursor) ([]Asset, *Cursor, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	args := []any{}
	where := "WHERE 1=1"
	i := 1

	if assetType != "" {
		where += fmt.Sprintf(" AND asset_type = $%d", i)
		args = append(args, assetType)
		i++
	}
	if source != "" {
		where += fmt.Sprintf(" AND source = $%d", i)
		args = append(args, source)
		i++
	}
	if after != nil {
		where += fmt.Sprintf(" AND (ingested_at, id) > ($%d, $%d)", i, i+1)
		args = append(args, after.IngestedAt, after.ID)
		i += 2
	}

	args = append(args, limit+1)
	q := fmt.Sprintf(`SELECT id, asset_type, source, external_id, name, ip_addresses, attributes, ingested_at, updated_at
		FROM assets %s ORDER BY ingested_at, id LIMIT $%d`, where, i)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	var assets []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.AssetType, &a.Source, &a.ExternalID, &a.Name,
			&a.IPAddresses, &a.Attributes, &a.IngestedAt, &a.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan asset: %w", err)
		}
		assets = append(assets, a)
	}

	var nextCursor *Cursor
	if len(assets) > limit {
		last := assets[limit-1]
		nextCursor = &Cursor{IngestedAt: last.IngestedAt, ID: last.ID}
		assets = assets[:limit]
	}
	return assets, nextCursor, nil
}

// UpsertRelationship inserts a relationship; ignores duplicate natural key.
// Returns (relationship, created, error). created=false when the edge already existed.
func (s *Store) UpsertRelationship(ctx context.Context, in RelationshipInput) (Relationship, bool, error) {
	// Verify both assets exist.
	for _, aid := range []uuid.UUID{in.SourceAssetID, in.TargetAssetID} {
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1)`, aid).Scan(&exists)
		if err != nil {
			return Relationship{}, false, fmt.Errorf("check asset %s: %w", aid, err)
		}
		if !exists {
			return Relationship{}, false, fmt.Errorf("%w: %s", ErrNotFound, aid)
		}
	}

	const q = `
INSERT INTO relationships (source_asset_id, target_asset_id, relationship_type, source, attributes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source_asset_id, target_asset_id, relationship_type) DO NOTHING
RETURNING id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at`

	row := s.pool.QueryRow(ctx, q,
		in.SourceAssetID, in.TargetAssetID, in.RelationshipType, in.Source,
		nullJSON(in.Attributes),
	)
	var r Relationship
	err := row.Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
		&r.Source, &r.Attributes, &r.IngestedAt)
	if err == pgx.ErrNoRows {
		// DO NOTHING path — fetch the existing row.
		err2 := s.pool.QueryRow(ctx,
			`SELECT id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at
             FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND relationship_type=$3`,
			in.SourceAssetID, in.TargetAssetID, in.RelationshipType,
		).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
			&r.Source, &r.Attributes, &r.IngestedAt)
		if err2 != nil {
			return Relationship{}, false, fmt.Errorf("fetch existing relationship: %w", err2)
		}
		return r, false, nil
	}
	if err != nil {
		return Relationship{}, false, fmt.Errorf("upsert relationship: %w", err)
	}
	return r, true, nil
}

// ListRelationships returns relationships for an asset (as source or target).
func (s *Store) ListRelationships(ctx context.Context, assetID uuid.UUID) ([]Relationship, error) {
	const q = `SELECT id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at
               FROM relationships WHERE source_asset_id=$1 OR target_asset_id=$1
               ORDER BY ingested_at`
	rows, err := s.pool.Query(ctx, q, assetID)
	if err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var r Relationship
		if err := rows.Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
			&r.Source, &r.Attributes, &r.IngestedAt); err != nil {
			return nil, fmt.Errorf("scan relationship: %w", err)
		}
		rels = append(rels, r)
	}
	return rels, nil
}

// GetRelationship fetches a specific edge by the natural key triple.
func (s *Store) GetRelationship(ctx context.Context, srcID, tgtID uuid.UUID, relType string) (Relationship, error) {
	const q = `SELECT id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at
               FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND relationship_type=$3`
	var r Relationship
	err := s.pool.QueryRow(ctx, q, srcID, tgtID, relType).Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
		&r.Source, &r.Attributes, &r.IngestedAt)
	if err == pgx.ErrNoRows {
		return Relationship{}, ErrNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship: %w", err)
	}
	return r, nil
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
