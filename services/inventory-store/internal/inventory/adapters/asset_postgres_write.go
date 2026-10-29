// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/operational-context-graph/inventory-store/internal/inventory"
)

// Upsert applies one asset delivery under the temporal-versioning contract and returns the
// resulting current version plus whether a brand-new asset was created (vs. a new version of an
// existing one).
//
// The adapter does persistence only. The domain decides the outcome:
//   - inventory.Update.IsRedeliveryOf — is this the same source change already stored?
//   - inventory.Update.ValidityBoundary — the contiguous close/open timestamp.
//
// Everything else here (FOR UPDATE lock, expire-then-insert, column order, null handling) mirrors
// the legacy store so the two write paths are row-for-row equivalent.
func (r *PostgresAssetRepository) Upsert(ctx context.Context, u inventory.Update) (*inventory.Asset, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	a, created, _, err := r.upsertTx(ctx, tx, u)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit upsert: %w", err)
	}
	return a, created, nil
}

// UpsertBatch applies many deliveries in a single transaction, each under the same contract.
// Results preserve input order. Any error rolls back the whole batch, matching the legacy store.
func (r *PostgresAssetRepository) UpsertBatch(ctx context.Context, updates []inventory.Update) ([]inventory.UpsertResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	results := make([]inventory.UpsertResult, 0, len(updates))
	for _, u := range updates {
		a, created, redelivery, err := r.upsertTx(ctx, tx, u)
		if err != nil {
			return nil, err
		}
		results = append(results, inventory.UpsertResult{Asset: a, Created: created, Redelivery: redelivery})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch: %w", err)
	}
	return results, nil
}

// upsertTx runs one delivery inside an open transaction. On re-delivery it returns the unchanged
// current asset with created=false, redelivery=true and performs no writes.
func (r *PostgresAssetRepository) upsertTx(ctx context.Context, tx pgx.Tx, u inventory.Update) (a *inventory.Asset, created, redelivery bool, err error) {
	// Load the current open version and lock it so concurrent upserts on the same key serialise.
	const checkCurrent = `SELECT ` + assetColumns + `
		FROM assets WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL
		FOR UPDATE`
	var curRow postgresAsset
	scanErr := scanAsset(tx.QueryRow(ctx, checkCurrent, u.Key.SourceSystem, u.Key.SourceID), &curRow)
	var current *inventory.Asset
	switch {
	case errors.Is(scanErr, pgx.ErrNoRows):
		current = nil
	case scanErr != nil:
		return nil, false, false, fmt.Errorf("check current asset: %w", scanErr)
	default:
		current, err = curRow.toDomain()
		if err != nil {
			return nil, false, false, fmt.Errorf("map current asset: %w", err)
		}
	}

	// Domain decision: an unchanged re-delivery writes nothing.
	if u.IsRedeliveryOf(current) {
		return current, false, true, nil
	}

	// Expire any current version at the domain-chosen boundary (NOW() when unknown), then insert
	// the new open version. created=true when there was no prior version to expire.
	const expire = `UPDATE assets SET valid_to = COALESCE($3, NOW())
		WHERE source_system = $1 AND source_id = $2 AND valid_to IS NULL`
	tag, err := tx.Exec(ctx, expire, u.Key.SourceSystem, u.Key.SourceID, u.ValidityBoundary())
	if err != nil {
		return nil, false, false, fmt.Errorf("expire asset: %w", err)
	}
	created = tag.RowsAffected() == 0

	const insert = `INSERT INTO assets (iri, name, artifact_type, source_system, source_id, extensions,
		ingested_at, created_at_source, last_updated_at_source, valid_from, valid_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($9, NOW()), NULL)
		RETURNING ` + assetColumns
	var newRow postgresAsset
	err = scanAsset(tx.QueryRow(ctx, insert,
		strOrNil(u.IRI), strOrNil(u.Name), u.ArtifactType, u.Key.SourceSystem, u.Key.SourceID,
		jsonOrEmpty(u.Extensions), u.IngestedAt, u.CreatedAtSource, u.LastUpdatedAtSource,
	), &newRow)
	if err != nil {
		return nil, false, false, fmt.Errorf("insert asset: %w", err)
	}
	a, err = newRow.toDomain()
	if err != nil {
		return nil, false, false, fmt.Errorf("map inserted asset: %w", err)
	}
	return a, created, false, nil
}

// strOrNil stores NULL instead of an empty string (mirrors store.nullStr).
func strOrNil(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// jsonOrEmpty stores an empty JSON object instead of NULL/empty (mirrors store.nullJSON).
func jsonOrEmpty(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
