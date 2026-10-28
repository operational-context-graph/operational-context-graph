// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/store"
	"github.com/stretchr/testify/require"
)

const ddl = `
CREATE TABLE IF NOT EXISTS assets (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_type           TEXT          NOT NULL,
    source_system           TEXT          NOT NULL,
    source_id               TEXT          NOT NULL,
    extensions              JSONB,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    created_at_source       TIMESTAMPTZ,
    last_updated_at_source  TIMESTAMPTZ,
    valid_from              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    valid_to                TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_assets_natural_key_current
    ON assets (source_system, source_id)
    WHERE valid_to IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_artifact_type
    ON assets (artifact_type)
    WHERE valid_to IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_created_at_id
    ON assets (created_at, id)
    WHERE valid_to IS NULL;
CREATE TABLE IF NOT EXISTS relationships (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id         UUID          NOT NULL REFERENCES assets(id),
    target_asset_id         UUID          NOT NULL REFERENCES assets(id),
    artifact_type           TEXT          NOT NULL,
    source_system           TEXT          NOT NULL,
    extensions              JSONB,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    created_at_source       TIMESTAMPTZ,
    last_updated_at_source  TIMESTAMPTZ,
    UNIQUE (source_asset_id, target_asset_id, artifact_type)
);
CREATE INDEX IF NOT EXISTS idx_relationships_source
    ON relationships (source_asset_id);
CREATE INDEX IF NOT EXISTS idx_relationships_target
    ON relationships (target_asset_id);
`

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
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

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(t.Context(), os.Getenv("TEST_DB_URL"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func cleanTables(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	_, err := testPool.Exec(ctx, "DELETE FROM relationships")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "DELETE FROM assets")
	require.NoError(t, err)
}

func TestPing(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.Ping(t.Context()))
}

func TestSchemaReady(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.SchemaReady(t.Context()))
}
