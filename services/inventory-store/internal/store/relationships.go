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

// relColumns is the canonical SELECT column list for the relationships table.
const relColumns = `id, source_asset_id, target_asset_id, artifact_type,
	source_system, source_key, target_key, extensions, ingested_at,
	created_at, created_at_source, last_updated_at_source, valid_from, valid_to`

func scanRelationship(row pgx.Row, r *Relationship) error {
	return row.Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.SourceKey, &r.TargetKey, &r.Extensions, &r.IngestedAt,
		&r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource, &r.ValidFrom, &r.ValidTo,
	)
}

func scanRelationshipRows(rows pgx.Rows, r *Relationship) error {
	return rows.Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.SourceKey, &r.TargetKey, &r.Extensions, &r.IngestedAt,
		&r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource, &r.ValidFrom, &r.ValidTo,
	)
}

// upsertRelationshipTx performs the expire-then-insert reconcile for one relationship inside tx.
//
// Temporal dedup: if the open row has the same last_updated_at_source as the incoming record,
// the row is returned unchanged (idempotent re-delivery). Otherwise the open row is expired
// and a new row is inserted so topology history is preserved.
func upsertRelationshipTx(ctx context.Context, tx pgx.Tx, in RelationshipInput) (Relationship, bool, error) {
	const checkCurrent = `
SELECT ` + relColumns + `
FROM relationships
WHERE source_key = $1 AND target_key = $2 AND artifact_type = $3 AND valid_to IS NULL
FOR UPDATE`

	var cur Relationship
	err := scanRelationship(tx.QueryRow(ctx, checkCurrent,
		nullStr(in.SourceKey), nullStr(in.TargetKey), in.ArtifactType,
	), &cur)
	rowExists := true
	if errors.Is(err, pgx.ErrNoRows) {
		rowExists = false
	} else if err != nil {
		return Relationship{}, false, fmt.Errorf("check current relationship: %w", err)
	}

	// Temporal dedup: skip new version if last_updated_at_source unchanged.
	if rowExists && in.LastUpdatedAtSource != nil && cur.LastUpdatedAtSource != nil {
		if cur.LastUpdatedAtSource.Equal(*in.LastUpdatedAtSource) {
			return cur, false, nil
		}
	}

	// Use last_updated_at_source as the temporal boundary when present.
	var validFrom *time.Time
	if in.LastUpdatedAtSource != nil {
		validFrom = in.LastUpdatedAtSource
	}

	// Expire the current open row. valid_to matches the new valid_from for a contiguous timeline.
	const expire = `
UPDATE relationships SET valid_to = COALESCE($4, NOW())
WHERE source_key = $1 AND target_key = $2 AND artifact_type = $3 AND valid_to IS NULL`
	tag, err := tx.Exec(ctx, expire,
		nullStr(in.SourceKey), nullStr(in.TargetKey), in.ArtifactType, validFrom,
	)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("expire relationship: %w", err)
	}
	created := tag.RowsAffected() == 0

	const insert = `
INSERT INTO relationships (source_asset_id, target_asset_id, artifact_type,
                           source_system, source_key, target_key,
                           extensions, ingested_at,
                           created_at_source, last_updated_at_source,
                           valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($10, NOW()), NULL)
RETURNING ` + relColumns

	var r Relationship
	err = scanRelationship(tx.QueryRow(ctx, insert,
		in.SourceAssetID, in.TargetAssetID, in.ArtifactType,
		in.SourceSystem, nullStr(in.SourceKey), nullStr(in.TargetKey),
		nullJSON(in.Extensions), in.IngestedAt,
		in.CreatedAtSource, in.LastUpdatedAtSource,
	), &r)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("insert relationship: %w", err)
	}
	return r, created, nil
}

// UpsertRelationship reconciles a relationship using expire-then-insert versioning.
//
// The logical key is (source_key, target_key, artifact_type). source_key and target_key
// are stable "{source_system}:{source_id}" strings that survive asset re-versioning.
//
// Behaviour:
//   - Re-delivery (same last_updated_at_source): returns existing row unchanged, created=false.
//   - Changed data (different last_updated_at_source): expires current row, inserts new version.
//   - No existing row: inserts new row, created=true.
func (s *Store) UpsertRelationship(ctx context.Context, in RelationshipInput) (Relationship, bool, error) {
	ids := []uuid.UUID{in.SourceAssetID, in.TargetAssetID}
	rows, err := s.pool.Query(ctx, `SELECT id FROM assets WHERE id = ANY($1) AND valid_to IS NULL`, ids)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("check assets: %w", err)
	}
	found := make(map[uuid.UUID]struct{}, 2)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Relationship{}, false, fmt.Errorf("scan asset id: %w", err)
		}
		found[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Relationship{}, false, fmt.Errorf("check assets: %w", err)
	}
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			return Relationship{}, false, fmt.Errorf("%w: %s", ErrAssetNotFound, id)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	r, created, err := upsertRelationshipTx(ctx, tx, in)
	if err != nil {
		return Relationship{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Relationship{}, false, fmt.Errorf("commit upsert relationship: %w", err)
	}
	return r, created, nil
}

// ListRelationships returns at most limit open relationships for an asset (as source or target).
// limit <= 0 defaults to 200; maximum 1000.
func (s *Store) ListRelationships(ctx context.Context, assetID uuid.UUID, limit int) ([]Relationship, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT ` + relColumns + `
               FROM relationships
               WHERE (source_asset_id=$1 OR target_asset_id=$1) AND valid_to IS NULL
               ORDER BY created_at LIMIT $2`
	rows, err := s.pool.Query(ctx, q, assetID, limit)
	if err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var r Relationship
		if err := scanRelationshipRows(rows, &r); err != nil {
			return nil, fmt.Errorf("scan relationship: %w", err)
		}
		rels = append(rels, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list relationships: %w", err)
	}
	return rels, nil
}

// GetRelationshipByID fetches a specific relationship by primary-key UUID (any version, open or closed).
func (s *Store) GetRelationshipByID(ctx context.Context, id uuid.UUID) (Relationship, error) {
	q := `SELECT ` + relColumns + ` FROM relationships WHERE id=$1`
	var r Relationship
	err := scanRelationship(s.pool.QueryRow(ctx, q, id), &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return Relationship{}, ErrRelationshipNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship by id: %w", err)
	}
	return r, nil
}

// FilterRelationships returns open relationships matching any combination of asset_id,
// source_asset_id, target_asset_id, and artifact_type.
// Omitting all filters returns all open rows. limit <= 0 defaults to 200; maximum 1000.
func (s *Store) FilterRelationships(ctx context.Context, assetID, sourceAssetID, targetAssetID *uuid.UUID, artifactType string, limit int) ([]Relationship, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	args := []any{}
	where := "WHERE valid_to IS NULL"
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
	q := fmt.Sprintf(`SELECT `+relColumns+`
               FROM relationships %s ORDER BY created_at LIMIT $%d`, where, i)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("filter relationships: %w", err)
	}
	defer rows.Close()

	var rels []Relationship
	for rows.Next() {
		var r Relationship
		if err := scanRelationshipRows(rows, &r); err != nil {
			return nil, fmt.Errorf("scan relationship: %w", err)
		}
		rels = append(rels, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filter relationships: %w", err)
	}
	return rels, nil
}

// GetRelationship fetches the current open edge by natural key triple.
func (s *Store) GetRelationship(ctx context.Context, srcID, tgtID uuid.UUID, artifactType string) (Relationship, error) {
	q := `SELECT ` + relColumns + `
               FROM relationships
               WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3 AND valid_to IS NULL`
	var r Relationship
	err := scanRelationship(s.pool.QueryRow(ctx, q, srcID, tgtID, artifactType), &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return Relationship{}, ErrRelationshipNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("get relationship: %w", err)
	}
	return r, nil
}

// UpsertRelationshipBatch reconciles multiple relationships in a single transaction.
// Returns 404 if any referenced current asset is missing.
func (s *Store) UpsertRelationshipBatch(ctx context.Context, inputs []RelationshipInput) ([]Relationship, error) {
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

	rels := make([]Relationship, 0, len(inputs))
	for _, in := range inputs {
		r, _, err := upsertRelationshipTx(ctx, tx, in)
		if err != nil {
			return nil, fmt.Errorf("upsert relationship batch row: %w", err)
		}
		rels = append(rels, r)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return rels, nil
}
