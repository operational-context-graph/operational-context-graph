// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// relColumns is the canonical SELECT column list for the relationships table.
const relColumns = `id, source_asset_id, target_asset_id, artifact_type,
	source_system, source_key, target_key, source_iri, target_iri, extensions, ingested_at,
	created_at, created_at_source, last_updated_at_source, valid_from, valid_to`

func scanRelationship(row pgx.Row, r *Relationship) error {
	return row.Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.SourceKey, &r.TargetKey, &r.SourceIRI, &r.TargetIRI, &r.Extensions, &r.IngestedAt,
		&r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource, &r.ValidFrom, &r.ValidTo,
	)
}

func scanRelationshipRows(rows pgx.Rows, r *Relationship) error {
	return rows.Scan(
		&r.ID, &r.SourceAssetID, &r.TargetAssetID, &r.ArtifactType,
		&r.SourceSystem, &r.SourceKey, &r.TargetKey, &r.SourceIRI, &r.TargetIRI, &r.Extensions, &r.IngestedAt,
		&r.CreatedAt, &r.CreatedAtSource, &r.LastUpdatedAtSource, &r.ValidFrom, &r.ValidTo,
	)
}

// upsertRelationshipTx reconciles one relationship inside tx using open/re-point semantics.
//
// Three cases, evaluated in order:
//  1. Re-delivery: open row exists with same last_updated_at_source → return unchanged, created=false.
//  2. Re-point: open row exists, last_updated_at_source changed → UPDATE endpoint UUIDs and
//     last_updated_at_source in place; the edge UUID and valid_from are preserved because the
//     topology (source_key/target_key) has not changed, only the asset version UUID has.
//  3. Open: no open row exists → INSERT new row, created=true.
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

	if rowExists {
		// Case 1: re-delivery — same timestamp, nothing to do.
		if in.LastUpdatedAtSource != nil && cur.LastUpdatedAtSource != nil &&
			cur.LastUpdatedAtSource.Equal(*in.LastUpdatedAtSource) {
			return cur, false, nil
		}

		// Case 2: re-point — topology unchanged, asset re-versioned.
		// Update endpoint UUIDs and last_updated_at_source in place; preserve edge UUID and valid_from.
		const repoint = `
UPDATE relationships
SET source_asset_id = $4, target_asset_id = $5, last_updated_at_source = $6
WHERE source_key = $1 AND target_key = $2 AND artifact_type = $3 AND valid_to IS NULL
RETURNING ` + relColumns
		var r Relationship
		err = scanRelationship(tx.QueryRow(ctx, repoint,
			nullStr(in.SourceKey), nullStr(in.TargetKey), in.ArtifactType,
			in.SourceAssetID, in.TargetAssetID, in.LastUpdatedAtSource,
		), &r)
		if err != nil {
			return Relationship{}, false, fmt.Errorf("re-point relationship: %w", err)
		}
		return r, false, nil
	}

	// Case 3: open — no existing row, insert new edge.
	const insert = `
INSERT INTO relationships (source_asset_id, target_asset_id, artifact_type,
                           source_system, source_key, target_key,
                           source_iri, target_iri,
                           extensions, ingested_at,
                           created_at_source, last_updated_at_source,
                           valid_from, valid_to)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($12, NOW()), NULL)
RETURNING ` + relColumns

	var r Relationship
	err = scanRelationship(tx.QueryRow(ctx, insert,
		in.SourceAssetID, in.TargetAssetID, in.ArtifactType,
		in.SourceSystem, nullStr(in.SourceKey), nullStr(in.TargetKey),
		nullStr(in.SourceIRI), nullStr(in.TargetIRI),
		nullJSON(in.Extensions), in.IngestedAt,
		in.CreatedAtSource, in.LastUpdatedAtSource,
	), &r)
	if err != nil {
		return Relationship{}, false, fmt.Errorf("insert relationship: %w", err)
	}
	return r, true, nil
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

// CloseRelationship sets valid_to = NOW() on an open relationship, recording when the edge
// disappeared from the source system. Returns ErrRelationshipNotFound if the row does not
// exist or is already closed.
func (s *Store) CloseRelationship(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE relationships SET valid_to = NOW() WHERE id = $1 AND valid_to IS NULL`, id)
	if err != nil {
		return fmt.Errorf("close relationship: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRelationshipNotFound
	}
	return nil
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

// resolveKeyTx looks up the current (valid_to IS NULL) asset UUID for a
// [source_system, source_id] key pair inside tx with FOR UPDATE to prevent races.
func resolveKeyTx(ctx context.Context, tx pgx.Tx, key []string) (uuid.UUID, error) {
	if len(key) != 2 {
		return uuid.Nil, fmt.Errorf("%w: key must be [source_system, source_id], got %v", ErrInvalidInput, key)
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM assets WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL FOR UPDATE`,
		key[0], key[1],
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: %s:%s", ErrAssetNotFound, key[0], key[1])
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve asset key %s:%s: %w", key[0], key[1], err)
	}
	return id, nil
}

// UpsertRelationshipBatchByKey resolves [source_system, source_id] key pairs to
// asset UUIDs inside a single transaction and then upserts the relationships.
// Returns ErrAssetNotFound if any referenced asset is missing.
func (s *Store) UpsertRelationshipBatchByKey(ctx context.Context, inputs []RelationshipKeyInput) ([]Relationship, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	cache := make(map[string]uuid.UUID)
	lookup := func(key []string) (uuid.UUID, error) {
		k := strings.Join(key, "\x00")
		if id, ok := cache[k]; ok {
			return id, nil
		}
		id, err := resolveKeyTx(ctx, tx, key)
		if err != nil {
			return uuid.Nil, err
		}
		cache[k] = id
		return id, nil
	}

	rels := make([]Relationship, 0, len(inputs))
	for _, in := range inputs {
		srcID, err := lookup(in.SourceKey)
		if err != nil {
			return nil, err
		}
		tgtID, err := lookup(in.TargetKey)
		if err != nil {
			return nil, err
		}
		resolved := RelationshipInput{
			SourceAssetID:       srcID,
			TargetAssetID:       tgtID,
			ArtifactType:        in.ArtifactType,
			SourceSystem:        in.SourceSystem,
			SourceKey:           strings.Join(in.SourceKey, ":"),
			TargetKey:           strings.Join(in.TargetKey, ":"),
			SourceIRI:           in.SourceIRI,
			TargetIRI:           in.TargetIRI,
			Extensions:          in.Extensions,
			IngestedAt:          in.IngestedAt,
			CreatedAtSource:     in.CreatedAtSource,
			LastUpdatedAtSource: in.LastUpdatedAtSource,
		}
		r, _, err := upsertRelationshipTx(ctx, tx, resolved)
		if err != nil {
			return nil, fmt.Errorf("upsert relationship batch by key row: %w", err)
		}
		rels = append(rels, r)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch by key: %w", err)
	}
	return rels, nil
}
