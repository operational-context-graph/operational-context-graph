// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
)

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
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, apperr.ErrNotFound
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
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, apperr.ErrNotFound
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
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}

	var nextCursor *Cursor
	if len(assets) > limit {
		last := assets[limit-1]
		nextCursor = &Cursor{IngestedAt: last.IngestedAt, ID: last.ID}
		assets = assets[:limit]
	}
	return assets, nextCursor, nil
}

// UpsertAssetBatch upserts multiple assets in a single transaction.
func (s *Store) UpsertAssetBatch(ctx context.Context, inputs []AssetInput) ([]BatchResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const q = `
INSERT INTO assets (asset_type, source, external_id, name, ip_addresses, attributes)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, external_id) DO UPDATE SET
    asset_type   = EXCLUDED.asset_type,
    name         = EXCLUDED.name,
    ip_addresses = EXCLUDED.ip_addresses,
    attributes   = EXCLUDED.attributes,
    updated_at   = NOW()
RETURNING id, (xmax = 0) AS inserted`

	results := make([]BatchResult, 0, len(inputs))
	for _, in := range inputs {
		var id uuid.UUID
		var inserted bool
		err := tx.QueryRow(ctx, q,
			in.AssetType, in.Source, in.ExternalID, in.Name,
			nullJSON(in.IPAddresses), nullJSON(in.Attributes),
		).Scan(&id, &inserted)
		if err != nil {
			return nil, fmt.Errorf("upsert asset batch row: %w", err)
		}
		outcome := "updated"
		if inserted {
			outcome = "created"
		}
		results = append(results, BatchResult{ID: id, Outcome: outcome})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return results, nil
}
