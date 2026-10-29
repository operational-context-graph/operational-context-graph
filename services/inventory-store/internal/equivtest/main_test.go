// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Package equivtest proves that the new repository read path returns results identical to the
// legacy store (DAO) path. It is a separate, additive test package: it imports both paths but
// changes neither. Run with: go test -tags integration ./internal/equivtest/ (needs TEST_DB_URL).
package equivtest

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ddl mirrors the real migrations (001_create_assets.sql) closely enough for the asset read
// path: it includes the iri and name columns the repository selects, plus the current-version
// partial unique index.
const ddl = `
CREATE TABLE IF NOT EXISTS assets (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    iri                    TEXT,
    name                   TEXT,
    artifact_type          VARCHAR(64)  NOT NULL,
    source_system          VARCHAR(128) NOT NULL,
    source_id              VARCHAR(256) NOT NULL,
    extensions             JSONB        NOT NULL DEFAULT '{}',
    ingested_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source      TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    valid_from             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to               TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_assets_natural_current
    ON assets (source_system, source_id) WHERE valid_to IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_cursor ON assets(created_at, id);
`

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		// No database configured: skip the whole package cleanly (matches store integration tests).
		os.Exit(0)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic("open pool: " + err.Error())
	}
	if _, err := pool.Exec(ctx, ddl); err != nil {
		panic("apply schema: " + err.Error())
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func cleanAssets(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(t.Context(), "DELETE FROM assets")
	if err != nil {
		t.Fatalf("clean assets: %v", err)
	}
}

// seedMissingID returns a random UUID that is guaranteed absent from the assets table, for
// exercising the not-found path. Call after cleanAssets so no collision is possible.
func seedMissingID(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.New()
}
