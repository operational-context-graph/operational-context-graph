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

// UpsertRelationship inserts a relationship; ignores duplicate natural key.
// Returns (relationship, created, error). created=false when the edge already existed.
func (s *Store) UpsertRelationship(ctx context.Context, in RelationshipInput) (Relationship, bool, error) {
	for _, aid := range []uuid.UUID{in.SourceAssetID, in.TargetAssetID} {
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1)`, aid).Scan(&exists)
		if err != nil {
			return Relationship{}, false, fmt.Errorf("check asset %s: %w", aid, err)
		}
		if !exists {
			return Relationship{}, false, fmt.Errorf("%w: %s", apperr.ErrNotFound, aid)
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
	if errors.Is(err, pgx.ErrNoRows) {
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
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
	if errors.Is(err, pgx.ErrNoRows) {
		return Relationship{}, apperr.ErrNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship: %w", err)
	}
	return r, nil
}

// UpsertRelationshipBatch inserts multiple relationships in a single transaction.
// Returns 404 if any referenced asset is missing.
func (s *Store) UpsertRelationshipBatch(ctx context.Context, inputs []RelationshipInput) ([]Relationship, error) {
	// Collect unique asset IDs and verify all exist in one round-trip.
	seen := make(map[uuid.UUID]struct{}, len(inputs)*2)
	for _, in := range inputs {
		seen[in.SourceAssetID] = struct{}{}
		seen[in.TargetAssetID] = struct{}{}
	}
	ids := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM assets WHERE id = ANY($1)`, ids)
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
			return nil, fmt.Errorf("%w: %s", apperr.ErrNotFound, id)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const q = `
INSERT INTO relationships (source_asset_id, target_asset_id, relationship_type, source, attributes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source_asset_id, target_asset_id, relationship_type) DO NOTHING
RETURNING id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at`

	rels := make([]Relationship, 0, len(inputs))
	for _, in := range inputs {
		var r Relationship
		err := tx.QueryRow(ctx, q,
			in.SourceAssetID, in.TargetAssetID, in.RelationshipType, in.Source,
			nullJSON(in.Attributes),
		).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
			&r.Source, &r.Attributes, &r.IngestedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			// DO NOTHING path — fetch existing row
			err2 := tx.QueryRow(ctx,
				`SELECT id, source_asset_id, target_asset_id, relationship_type, source, attributes, ingested_at
				 FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND relationship_type=$3`,
				in.SourceAssetID, in.TargetAssetID, in.RelationshipType,
			).Scan(&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.RelationshipType,
				&r.Source, &r.Attributes, &r.IngestedAt)
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
