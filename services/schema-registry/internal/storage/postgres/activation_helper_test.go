// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var errActivationConflict = errors.New("activation predecessor conflict")

// activateTestRelease is privileged test-only code. Production API packages do
// not import it and therefore cannot activate or otherwise mutate registry data.
func activateTestRelease(ctx context.Context, conn *pgx.Conn, candidate string, expectedPrevious *string) error {
	return activateTestReleaseWithFailure(ctx, conn, candidate, expectedPrevious, nil)
}

func activateTestReleaseWithFailure(ctx context.Context, conn *pgx.Conn, candidate string, expectedPrevious *string, inject func(pgx.Tx) error) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current *string
	if err := tx.QueryRow(ctx, `SELECT current_schema_version FROM schema_registry.registry_state WHERE singleton = true FOR UPDATE`).Scan(&current); err != nil {
		return err
	}
	if !sameString(current, expectedPrevious) {
		return errActivationConflict
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(activated_sequence), 0) + 1 FROM schema_registry.releases`).Scan(&sequence); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
UPDATE schema_registry.releases
SET activated_sequence = $2
WHERE schema_version = $1 AND activated_sequence IS NULL`, candidate, sequence)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("candidate release is unavailable: %w", errActivationConflict)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config('schema_registry.activation_sequence', $1, true)", fmt.Sprint(sequence)); err != nil {
		return err
	}
	if inject != nil {
		if err := inject(tx); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO schema_registry.activated_revision_reachability (artifact_revision, first_activated_sequence)
SELECT artifact_revision, $2
FROM schema_registry.release_artifacts
WHERE schema_version = $1
ON CONFLICT (artifact_revision) DO NOTHING`, candidate, sequence); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE schema_registry.registry_state SET current_schema_version = $1 WHERE singleton = true`, candidate); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sameString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
