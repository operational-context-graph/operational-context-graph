// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertAsset upserts an asset using the temporal dedup contract:
//   - If the natural key exists and last_updated_at_source matches → re-delivery, no new version row.
//   - If last_updated_at_source advances, or the asset is new → expire current row, insert new version.
//
// Returns (asset, created, error). created=true when a new row was inserted.
func (s *Store) UpsertAsset(ctx context.Context, in AssetInput) (Asset, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Check for existing current row and compare last_updated_at_source.
	const checkCurrent = `
SELECT id, last_updated_at_source
FROM assets
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`

	var cur struct {
		id                  uuid.UUID
		lastUpdatedAtSource *time.Time
	}
	err = tx.QueryRow(ctx, checkCurrent, in.SourceSystem, in.SourceID).Scan(&cur.id, &cur.lastUpdatedAtSource)
	rowExists := true
	if errors.Is(err, pgx.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return Asset{}, false, fmt.Errorf("check current asset: %w", err)
	}

	// Temporal dedup: skip new version if last_updated_at_source unchanged.
	if rowExists && in.LastUpdatedAtSource != nil && cur.lastUpdatedAtSource != nil {
		if cur.lastUpdatedAtSource.Equal(*in.LastUpdatedAtSource) {
			// Re-delivery — return existing current row without creating a new version.
			if err := tx.Commit(ctx); err != nil {
				return Asset{}, false, fmt.Errorf("commit redelivery tx: %w", err)
			}
			asset, fetchErr := s.GetAsset(ctx, cur.id)
			if fetchErr != nil {
				return Asset{}, false, fetchErr
			}
			return asset, false, nil
		}
	}

	// Expire any current version with the same natural key.
	const expire = `
UPDATE assets SET valid_to = NOW()
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`
	tag, err := tx.Exec(ctx, expire, in.SourceSystem, in.SourceID)
	if err != nil {
		return Asset{}, false, fmt.Errorf("expire asset: %w", err)
	}
	created := tag.RowsAffected() == 0

	const insert = `
INSERT INTO assets (artifact_type, source_system, source_id, extensions,
                    created_at_source, last_updated_at_source, valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NULL)
RETURNING id, artifact_type, source_system, source_id, extensions,
          created_at, created_at_source, last_updated_at_source, valid_from, valid_to`

	row := tx.QueryRow(ctx, insert,
		in.ArtifactType, in.SourceSystem, in.SourceID,
		nullJSON(in.Extensions),
		in.CreatedAtSource, in.LastUpdatedAtSource,
	)
	var a Asset
	err = row.Scan(
		&a.ID, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
		&a.Extensions, &a.CreatedAt,
		&a.CreatedAtSource, &a.LastUpdatedAtSource, &a.ValidFrom, &a.ValidTo,
	)
	if err != nil {
		return Asset{}, false, fmt.Errorf("insert asset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Asset{}, false, fmt.Errorf("commit upsert asset: %w", err)
	}
	return a, created, nil
}

// GetAsset fetches the current version of an asset by internal UUID.
func (s *Store) GetAsset(ctx context.Context, id uuid.UUID) (Asset, error) {
	const q = `SELECT id, artifact_type, source_system, source_id, extensions,
               created_at, created_at_source, last_updated_at_source, valid_from, valid_to
               FROM assets WHERE id = $1 AND valid_to IS NULL`
	row := s.pool.QueryRow(ctx, q, id)
	var a Asset
	err := row.Scan(&a.ID, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
		&a.Extensions, &a.CreatedAt,
		&a.CreatedAtSource, &a.LastUpdatedAtSource, &a.ValidFrom, &a.ValidTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrAssetNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

// ListAssets returns a page of current assets filtered by artifact_type or source_system.
func (s *Store) ListAssets(ctx context.Context, artifactType, sourceSystem string, limit int, after *Cursor) ([]Asset, *Cursor, error) {
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
	if after != nil {
		where += fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", i, i+1)
		args = append(args, after.CreatedAt, after.ID)
		i += 2
	}

	args = append(args, limit+1)
	q := fmt.Sprintf(`SELECT id, artifact_type, source_system, source_id, extensions,
		created_at, created_at_source, last_updated_at_source, valid_from, valid_to
		FROM assets %s ORDER BY created_at, id LIMIT $%d`, where, i)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	var assets []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
			&a.Extensions, &a.CreatedAt,
			&a.CreatedAtSource, &a.LastUpdatedAtSource, &a.ValidFrom, &a.ValidTo); err != nil {
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
		nextCursor = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		assets = assets[:limit]
	}
	return assets, nextCursor, nil
}

// UpsertAssetBatch upserts multiple assets using the temporal dedup model.
func (s *Store) UpsertAssetBatch(ctx context.Context, inputs []AssetInput) ([]BatchResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const checkCurrent = `
SELECT id, last_updated_at_source
FROM assets
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`

	const expire = `
UPDATE assets SET valid_to = NOW()
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`

	const insert = `
INSERT INTO assets (artifact_type, source_system, source_id, extensions,
                    created_at_source, last_updated_at_source, valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NULL)
RETURNING id`

	results := make([]BatchResult, 0, len(inputs))
	for _, in := range inputs {
		// Temporal dedup check.
		var curID uuid.UUID
		var curLUAS *time.Time
		err := tx.QueryRow(ctx, checkCurrent, in.SourceSystem, in.SourceID).Scan(&curID, &curLUAS)
		rowExists := true
		if errors.Is(err, pgx.ErrNoRows) {
			rowExists = false
		} else if err != nil {
			return nil, fmt.Errorf("check current asset in batch: %w", err)
		}

		if rowExists && in.LastUpdatedAtSource != nil && curLUAS != nil {
			if curLUAS.Equal(*in.LastUpdatedAtSource) {
				results = append(results, BatchResult{ID: curID, Outcome: "updated"})
				continue
			}
		}

		tag, err := tx.Exec(ctx, expire, in.SourceSystem, in.SourceID)
		if err != nil {
			return nil, fmt.Errorf("expire asset batch row: %w", err)
		}
		created := tag.RowsAffected() == 0

		var id uuid.UUID
		if err := tx.QueryRow(ctx, insert,
			in.ArtifactType, in.SourceSystem, in.SourceID,
			nullJSON(in.Extensions),
			in.CreatedAtSource, in.LastUpdatedAtSource,
		).Scan(&id); err != nil {
			return nil, fmt.Errorf("insert asset batch row: %w", err)
		}
		outcome := "updated"
		if created {
			outcome = "created"
		}
		results = append(results, BatchResult{ID: id, Outcome: outcome})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return results, nil
}
