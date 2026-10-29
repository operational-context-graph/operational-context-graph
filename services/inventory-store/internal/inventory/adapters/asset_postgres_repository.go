// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/operational-context-graph/inventory-store/internal/inventory"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// assetColumns is the canonical SELECT column list for the assets table, in scan order.
const assetColumns = `id, iri, name, artifact_type, source_system, source_id, extensions,
	ingested_at, created_at, created_at_source, last_updated_at_source, valid_from, valid_to`

// PostgresAssetRepository reads and writes assets in PostgreSQL via the domain types.
//
// This is the repository-pattern counterpart to the legacy store.Store DAO. Its SQL mirrors the
// legacy path so the two can be compared behind the read-path feature flag. Reads live here;
// the temporal-versioning writes live in asset_postgres_write.go.
type PostgresAssetRepository struct {
	pool *pgxpool.Pool
}

// Compile-time proof that the Postgres adapter satisfies the domain ports. If a signature drifts
// from the interface, this fails to build instead of surfacing at a call site.
var _ inventory.AssetRepository = (*PostgresAssetRepository)(nil)

// NewPostgresAssetRepository builds a repository over an existing connection pool.
func NewPostgresAssetRepository(pool *pgxpool.Pool) *PostgresAssetRepository {
	return &PostgresAssetRepository{pool: pool}
}

func scanAsset(row pgx.Row, r *postgresAsset) error {
	return row.Scan(
		&r.ID, &r.IRI, &r.Name, &r.ArtifactType, &r.SourceSystem, &r.SourceID,
		&r.Extensions, &r.IngestedAt, &r.CreatedAt,
		&r.CreatedAtSource, &r.LastUpdatedAtSource, &r.ValidFrom, &r.ValidTo,
	)
}

// GetAsset fetches the current version of an asset by internal UUID.
func (r *PostgresAssetRepository) GetAsset(ctx context.Context, id uuid.UUID) (*inventory.Asset, error) {
	const q = `SELECT ` + assetColumns + `
		FROM assets WHERE id = $1 AND valid_to IS NULL`
	var row postgresAsset
	err := scanAsset(r.pool.QueryRow(ctx, q, id), &row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrAssetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	return row.toDomain()
}

// ListAssets returns a page of current assets filtered by artifact_type, source_system, or
// source_id, with keyset pagination identical to the legacy store path.
func (r *PostgresAssetRepository) ListAssets(
	ctx context.Context,
	artifactType, sourceSystem, sourceID string,
	limit int,
	after *inventory.Cursor,
) ([]*inventory.Asset, *inventory.Cursor, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	args := []any{}
	where := "WHERE valid_to IS NULL"
	i := 1

	if artifactType != "" {
		where += fmt.Sprintf(" AND artifact_type = $%d", i)
		args = append(args, artifactType)
		i++
	}
	if sourceSystem != "" {
		where += fmt.Sprintf(" AND source_system = $%d", i)
		args = append(args, sourceSystem)
		i++
	}
	if sourceID != "" {
		where += fmt.Sprintf(" AND source_id = $%d", i)
		args = append(args, sourceID)
		i++
	}
	if after != nil {
		where += fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", i, i+1)
		args = append(args, after.CreatedAt, after.ID)
		i += 2
	}

	args = append(args, limit+1)
	q := fmt.Sprintf(`SELECT `+assetColumns+`
		FROM assets %s ORDER BY created_at, id LIMIT $%d`, where, i)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	var assets []*inventory.Asset
	for rows.Next() {
		var row postgresAsset
		if err := scanAsset(rows, &row); err != nil {
			return nil, nil, fmt.Errorf("scan asset: %w", err)
		}
		a, err := row.toDomain()
		if err != nil {
			return nil, nil, fmt.Errorf("map asset: %w", err)
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}

	var nextCursor *inventory.Cursor
	if len(assets) > limit {
		last := assets[limit-1]
		nextCursor = &inventory.Cursor{CreatedAt: last.CreatedAt(), ID: last.ID()}
		assets = assets[:limit]
	}
	return assets, nextCursor, nil
}
