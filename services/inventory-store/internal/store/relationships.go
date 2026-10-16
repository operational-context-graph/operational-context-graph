// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertRelationship inserts a relationship; silently drops duplicate natural key.
// Returns (relationship, created, error). created=false when the edge already existed.
func (s *Store) UpsertRelationship(ctx context.Context, in RelationshipInput) (Relationship, bool, error) {
	for _, aid := range []uuid.UUID{in.SourceAssetID, in.TargetAssetID} {
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1 AND valid_to IS NULL)`, aid).Scan(&exists)
		if err != nil {
			return Relationship{}, false, fmt.Errorf("check asset %s: %w", aid, err)
		}
		if !exists {
			return Relationship{}, false, fmt.Errorf("%w: %s", ErrAssetNotFound, aid)
		}
	}

	const q = `
INSERT INTO relationships (source_asset_id, target_asset_id, artifact_type, source_system, extensions,
                           created_at_source, last_updated_at_source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (source_asset_id, target_asset_id, artifact_type) DO NOTHING
RETURNING id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
          created_at, created_at_source, last_updated_at_source`

	row := s.pool.QueryRow(ctx, q,
		in.SourceAssetID, in.TargetAssetID, in.ArtifactType, in.SourceSystem,
		nullJSON(in.Extensions), in.CreatedAtSource, in.LastUpdatedAtSource,
	)
	var r Relationship
	err := row.Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
	if errors.Is(err, pgx.ErrNoRows) {
		// DO NOTHING path — fetch the existing row and return 200.
		err2 := s.pool.QueryRow(ctx,
			`SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
             created_at, created_at_source, last_updated_at_source
             FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3`,
			in.SourceAssetID, in.TargetAssetID, in.ArtifactType,
		).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
			&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
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

// ListRelationships returns at most limit relationships for an asset (as source or target).
// limit <= 0 defaults to 200; maximum 1000.
func (s *Store) ListRelationships(ctx context.Context, assetID uuid.UUID, limit int) ([]Relationship, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	const q = `SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
               created_at, created_at_source, last_updated_at_source
               FROM relationships WHERE source_asset_id=$1 OR target_asset_id=$1
               ORDER BY created_at LIMIT $2`
	rows, err := s.pool.Query(ctx, q, assetID, limit)
	if err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var r Relationship
		if err := rows.Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
			&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource); err != nil {
			return nil, fmt.Errorf("scan relationship: %w", err)
		}
		rels = append(rels, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	return rels, nil
}

// GetRelationshipByID fetches a specific relationship by its primary-key UUID.
func (s *Store) GetRelationshipByID(ctx context.Context, id uuid.UUID) (Relationship, error) {
	const q = `SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
               created_at, created_at_source, last_updated_at_source
               FROM relationships WHERE id=$1`
	var r Relationship
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return Relationship{}, ErrRelationshipNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship by id: %w", err)
	}
	return r, nil
}

// FilterRelationships returns relationships matching any combination of asset_id, source_asset_id,
// target_asset_id, and artifact_type.
// Omitting all filters returns all rows. limit <= 0 defaults to 200; maximum 1000.
func (s *Store) FilterRelationships(ctx context.Context, assetID, sourceAssetID, targetAssetID *uuid.UUID, artifactType string, limit int) ([]Relationship, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	args := []any{}
	where := "WHERE 1=1"
	i := 1

	if assetID != nil {
		where += fmt.Sprintf(" AND (source_asset_id=$%d OR target_asset_id=$%d)", i, i+1)
		args = append(args, *assetID, *assetID)
		i += 2
	}
	if sourceAssetID != nil {
		where += fmt.Sprintf(" AND source_asset_id=$%d", i)
		args = append(args, *sourceAssetID)
		i++
	}
	if targetAssetID != nil {
		where += fmt.Sprintf(" AND target_asset_id=$%d", i)
		args = append(args, *targetAssetID)
		i++
	}
	if artifactType != "" {
		where += fmt.Sprintf(" AND artifact_type=$%d", i)
		args = append(args, artifactType)
		i++
	}

	args = append(args, limit)
	q := fmt.Sprintf(`SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
               created_at, created_at_source, last_updated_at_source
               FROM relationships %s ORDER BY created_at LIMIT $%d`, where, i)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("filter relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var r Relationship
		if err := rows.Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
			&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource); err != nil {
			return nil, fmt.Errorf("scan relationship: %w", err)
		}
		rels = append(rels, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filter relationships: %w", err)
	}
	return rels, nil
}

// GetRelationship fetches a specific edge by the natural key triple.
func (s *Store) GetRelationship(ctx context.Context, srcID, tgtID uuid.UUID, artifactType string) (Relationship, error) {
	const q = `SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
               created_at, created_at_source, last_updated_at_source
               FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3`
	var r Relationship
	err := s.pool.QueryRow(ctx, q, srcID, tgtID, artifactType).Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return Relationship{}, ErrRelationshipNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship: %w", err)
	}
	return r, nil
}

// UpsertRelationshipBatch inserts multiple relationships in a single transaction.
// Returns 404 if any referenced current asset is missing.
func (s *Store) UpsertRelationshipBatch(ctx context.Context, inputs []RelationshipInput) ([]Relationship, error) {
	// Collect unique asset IDs and verify all exist (current version only) in one round-trip.
	seen := make(map[uuid.UUID]struct{}, len(inputs)*2)
	for _, in := range inputs {
		seen[in.SourceAssetID] = struct{}{}
		seen[in.TargetAssetID] = struct{}{}
	}
	ids := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM assets WHERE id = ANY($1) AND valid_to IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("bulk check assets: %w", err)
	}
	found := make(map[uuid.UUID]struct{}, len(ids))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan asset id: %w", err)
		}
		found[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk check assets: %w", err)
	}
	for id := range seen {
		if _, ok := found[id]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrAssetNotFound, id)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const q = `
INSERT INTO relationships (source_asset_id, target_asset_id, artifact_type, source_system, extensions,
                           created_at_source, last_updated_at_source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (source_asset_id, target_asset_id, artifact_type) DO NOTHING
RETURNING id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
          created_at, created_at_source, last_updated_at_source`

	rels := make([]Relationship, 0, len(inputs))
	for _, in := range inputs {
		var r Relationship
		err := tx.QueryRow(ctx, q,
			in.SourceAssetID, in.TargetAssetID, in.ArtifactType, in.SourceSystem,
			nullJSON(in.Extensions), in.CreatedAtSource, in.LastUpdatedAtSource,
		).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
			&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
		if errors.Is(err, pgx.ErrNoRows) {
			// DO NOTHING path — fetch existing row
			err2 := tx.QueryRow(ctx,
				`SELECT id, source_asset_id, target_asset_id, artifact_type, source_system, extensions,
				 created_at, created_at_source, last_updated_at_source
				 FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3`,
				in.SourceAssetID, in.TargetAssetID, in.ArtifactType,
			).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
				&r.SourceSystem, &r.Extensions, &r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource)
			if err2 != nil {
				return nil, fmt.Errorf("fetch existing relationship in batch: %w", err2)
			}
		} else if err != nil {
			return nil, fmt.Errorf("upsert relationship batch row: %w", err)
		}
		rels = append(rels, r)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return rels, nil
}
