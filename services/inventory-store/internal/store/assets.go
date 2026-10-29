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
// valid_from is set to last_updated_at_source when present (source-time bitemporality), otherwise NOW().
// valid_to on the expiring row is set to the same timestamp for a consistent temporal boundary.
//
// Returns (asset, created, error). created=true when a new row was inserted.
func (s *Store) UpsertAsset(ctx context.Context, in AssetInput) (Asset, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // Rollback is a no-op after Commit.

	// Check for existing current row and compare last_updated_at_source.
	// FOR UPDATE serialises concurrent upserts on the same natural key.
	const checkCurrent = `
SELECT id, iri, name, artifact_type, source_system, source_id, extensions,
       ingested_at, created_at, created_at_source, last_updated_at_source, valid_from, valid_to
FROM assets
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL
FOR UPDATE`

	var cur Asset
	err = tx.QueryRow(ctx, checkCurrent, in.SourceSystem, in.SourceID).Scan(
		&cur.ID, &cur.IRI, &cur.Name, &cur.ArtifactType, &cur.SourceSystem, &cur.SourceID,
		&cur.Extensions, &cur.IngestedAt, &cur.CreatedAt,
		&cur.CreatedAtSource, &cur.LastUpdatedAtSource, &cur.ValidFrom, &cur.ValidTo,
	)
	rowExists := true
	if errors.Is(err, pgx.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return Asset{}, false, fmt.Errorf("check current asset: %w", err)
	}

	// Temporal dedup: skip new version if last_updated_at_source unchanged.
	if rowExists && in.LastUpdatedAtSource != nil && cur.LastUpdatedAtSource != nil {
		if cur.LastUpdatedAtSource.Equal(*in.LastUpdatedAtSource) {
			// Re-delivery — commit (no writes) and return the already-scanned row.
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return Asset{}, false, fmt.Errorf("commit redelivery tx: %w", commitErr)
			}
			return cur, false, nil
		}
	}

	// Compute the timestamp for valid_from on the new row and valid_to on the expiring row.
	// Use last_updated_at_source when present so the temporal boundary reflects the source change time.
	var validFrom *time.Time
	if in.LastUpdatedAtSource != nil {
		validFrom = in.LastUpdatedAtSource
	}

	// Expire any current version. valid_to is set to the same timestamp as the new valid_from
	// so the temporal ranges are contiguous with no gap or overlap.
	const expire = `
UPDATE assets SET valid_to = COALESCE($3, NOW())
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`
	tag, err := tx.Exec(ctx, expire, in.SourceSystem, in.SourceID, validFrom)
	if err != nil {
		return Asset{}, false, fmt.Errorf("expire asset: %w", err)
	}
	created := tag.RowsAffected() == 0

	const insert = `
INSERT INTO assets (iri, name, artifact_type, source_system, source_id, extensions,
                    ingested_at, created_at_source, last_updated_at_source,
                    valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($9, NOW()), NULL)
RETURNING id, iri, name, artifact_type, source_system, source_id, extensions,
          ingested_at, created_at, created_at_source, last_updated_at_source, valid_from, valid_to`

	row := tx.QueryRow(ctx, insert,
		nullStr(in.IRI), nullStr(in.Name), in.ArtifactType, in.SourceSystem, in.SourceID,
		nullJSON(in.Extensions),
		in.IngestedAt, in.CreatedAtSource, in.LastUpdatedAtSource,
	)
	var a Asset
	err = row.Scan(
		&a.ID, &a.IRI, &a.Name, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
		&a.Extensions, &a.IngestedAt, &a.CreatedAt,
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
	const q = `SELECT id, iri, name, artifact_type, source_system, source_id, extensions,
               ingested_at, created_at, created_at_source, last_updated_at_source, valid_from, valid_to
               FROM assets WHERE id = $1 AND valid_to IS NULL`
	row := s.pool.QueryRow(ctx, q, id)
	var a Asset
	err := row.Scan(&a.ID, &a.IRI, &a.Name, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
		&a.Extensions, &a.IngestedAt, &a.CreatedAt,
		&a.CreatedAtSource, &a.LastUpdatedAtSource, &a.ValidFrom, &a.ValidTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrAssetNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

// ListAssets returns a page of current assets filtered by artifact_type, source_system, or source_id.
func (s *Store) ListAssets(ctx context.Context, artifactType, sourceSystem, sourceID string, limit int, after *Cursor) ([]Asset, *Cursor, error) {
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
	q := fmt.Sprintf(`SELECT id, iri, name, artifact_type, source_system, source_id, extensions,
		ingested_at, created_at, created_at_source, last_updated_at_source, valid_from, valid_to
		FROM assets %s ORDER BY created_at, id LIMIT $%d`, where, i)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	var assets []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.IRI, &a.Name, &a.ArtifactType, &a.SourceSystem, &a.SourceID,
			&a.Extensions, &a.IngestedAt, &a.CreatedAt,
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
	defer func() { _ = tx.Rollback(ctx) }() // Rollback is a no-op after Commit.

	const checkCurrent = `
SELECT id, last_updated_at_source
FROM assets
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL
FOR UPDATE`

	const expire = `
UPDATE assets SET valid_to = COALESCE($3, NOW())
WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`

	const insert = `
INSERT INTO assets (iri, name, artifact_type, source_system, source_id, extensions,
                    ingested_at, created_at_source, last_updated_at_source,
                    valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($9, NOW()), NULL)
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
				results = append(results, BatchResult{ID: curID, Outcome: "unchanged"})
				continue
			}
		}

		var validFrom *time.Time
		if in.LastUpdatedAtSource != nil {
			validFrom = in.LastUpdatedAtSource
		}

		tag, err := tx.Exec(ctx, expire, in.SourceSystem, in.SourceID, validFrom)
		if err != nil {
			return nil, fmt.Errorf("expire asset batch row: %w", err)
		}
		created := tag.RowsAffected() == 0

		var id uuid.UUID
		if err := tx.QueryRow(ctx, insert,
			nullStr(in.IRI), nullStr(in.Name), in.ArtifactType, in.SourceSystem, in.SourceID,
			nullJSON(in.Extensions),
			in.IngestedAt, in.CreatedAtSource, in.LastUpdatedAtSource,
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
