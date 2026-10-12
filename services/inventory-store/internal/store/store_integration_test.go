// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ddl = `
CREATE TABLE IF NOT EXISTS assets (
    id                   UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_type        VARCHAR(64)  NOT NULL,
    source_system        VARCHAR(128) NOT NULL,
    source_id            VARCHAR(256) NOT NULL,
    name                 VARCHAR(256),
    ip_addresses         JSONB,
    extensions           JSONB,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source    TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    valid_from           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to             TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_current
    ON assets (source_system, source_id)
    WHERE valid_to IS NULL;
CREATE TABLE IF NOT EXISTS relationships (
    id                   UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id      UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    target_asset_id      UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    artifact_type        VARCHAR(64)  NOT NULL,
    source_system        VARCHAR(128) NOT NULL,
    extensions           JSONB,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source    TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    UNIQUE (source_asset_id, target_asset_id, artifact_type)
);`

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
	s, err := store.New(context.Background(), os.Getenv("TEST_DB_URL"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func cleanTables(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "DELETE FROM relationships")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, "DELETE FROM assets")
	require.NoError(t, err)
}

func TestPing(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.Ping(context.Background()))
}

func TestSchemaReady(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.SchemaReady(context.Background()))
}

func TestUpsertAsset_Created(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	in := store.AssetInput{
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "srv-001",
	}
	a, created, err := s.UpsertAsset(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, uuid.Nil, a.ID)
	assert.Equal(t, "server", a.ArtifactType)
}

func TestUpsertAsset_Idempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "srv-002",
		LastUpdatedAtSource: &ts,
	}

	a1, created1, err := s.UpsertAsset(context.Background(), in)
	require.NoError(t, err)
	require.True(t, created1)

	a2, created2, err := s.UpsertAsset(context.Background(), in)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, a1.ID, a2.ID, "re-delivery must return same row ID")

	// Verify only one current row exists.
	var count int
	err = testPool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM assets WHERE source_system=$1 AND source_id=$2 AND valid_to IS NULL",
		"cmdb", "srv-002").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUpsertAsset_ObservedAtGuard_ExactMatchIsIdempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := context.Background()
	ts := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "srv-003",
		LastUpdatedAtSource: &ts,
	}
	a1, created1, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	require.True(t, created1)

	// Re-deliver with the identical timestamp — must return the same row, no new version.
	a2, created2, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.False(t, created2, "exact-match re-delivery must not create a new version")
	assert.Equal(t, a1.ID, a2.ID, "exact-match re-delivery must return the same row ID")

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM assets WHERE source_system=$1 AND source_id=$2",
		"cmdb", "srv-003").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "only one row must exist — no expired history for an exact-match re-delivery")
}

func TestUpsertAsset_NewerTimestamp_CreatesNewVersion(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ts1 := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	ts2 := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "srv-004",
		LastUpdatedAtSource: &ts1,
	}
	a1, _, err := s.UpsertAsset(context.Background(), in)
	require.NoError(t, err)

	in.LastUpdatedAtSource = &ts2
	a2, created2, err := s.UpsertAsset(context.Background(), in)
	require.NoError(t, err)
	assert.False(t, created2, "upsert of existing natural key is not a net-new row (created=false means it replaced)")
	assert.NotEqual(t, a1.ID, a2.ID, "a new row must be created for the newer timestamp")

	// Old row must be expired.
	var validTo *time.Time
	err = testPool.QueryRow(context.Background(),
		"SELECT valid_to FROM assets WHERE id=$1", a1.ID).Scan(&validTo)
	require.NoError(t, err)
	assert.NotNil(t, validTo, "old row must have valid_to set")
}

func TestGetAsset_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetAsset(context.Background(), uuid.New())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestUpsertRelationship_Idempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := context.Background()

	src := store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "src-rel-001"}
	tgt := store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "tgt-rel-001"}
	aSrc, _, err := s.UpsertAsset(ctx, src)
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, tgt)
	require.NoError(t, err)

	in := store.RelationshipInput{
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
	}
	r1, created1, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.True(t, created1)

	r2, created2, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, r1.ID, r2.ID)

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3",
		aSrc.ID, aTgt.ID, "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestFilterRelationships_ByType(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := context.Background()

	src := store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "src-filter-001"}
	tgt := store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "tgt-filter-001"}
	aSrc, _, err := s.UpsertAsset(ctx, src)
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, tgt)
	require.NoError(t, err)

	_, _, err = s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
	})
	require.NoError(t, err)
	_, _, err = s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "depends_on", SourceSystem: "cmdb",
	})
	require.NoError(t, err)

	artifactType := "connects"
	results, err := s.FilterRelationships(ctx, nil, nil, nil, artifactType, 100)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "connects", results[0].ArtifactType)
}
