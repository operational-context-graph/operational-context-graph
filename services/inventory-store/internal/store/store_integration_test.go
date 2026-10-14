// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/operational-context-graph/inventory-store/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		os.Exit(0)
	}
	ctx := context.Background()

	// Run migrations using the real migration files so the test schema
	// always matches production and cannot drift from the inline DDL.
	sqlDB, err := sql.Open("pgx", dbURL)
	if err != nil {
		panic("open sql db for migrations: " + err.Error())
	}
	if err := sqlDB.Ping(); err != nil {
		panic("ping db for migrations: " + err.Error())
	}
	provider, err := migrations.NewProvider(sqlDB)
	if err != nil {
		panic("create goose provider: " + err.Error())
	}
	if _, err := provider.Up(ctx); err != nil {
		panic("apply migrations: " + err.Error())
	}
	sqlDB.Close()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic("open pool: " + err.Error())
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

func TestUpsertAsset_Created(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	in := store.AssetInput{
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "srv-001",
	}
	a, created, err := s.UpsertAsset(t.Context(), in)
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

	a1, created1, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)
	require.True(t, created1)

	a2, created2, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, a1.ID, a2.ID, "re-delivery must return same row ID")

	// Verify only one current row exists.
	var count int
	err = testPool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM assets WHERE source_system=$1 AND source_id=$2 AND valid_to IS NULL",
		"cmdb", "srv-002").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUpsertAsset_ObservedAtGuard_ExactMatchIsIdempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()
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
	a1, _, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)

	in.LastUpdatedAtSource = &ts2
	a2, created2, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)
	assert.False(t, created2, "upsert of existing natural key is not a net-new row (created=false means it replaced)")
	assert.NotEqual(t, a1.ID, a2.ID, "a new row must be created for the newer timestamp")

	// Old row must be expired.
	var validTo *time.Time
	err = testPool.QueryRow(t.Context(),
		"SELECT valid_to FROM assets WHERE id=$1", a1.ID).Scan(&validTo)
	require.NoError(t, err)
	assert.NotNil(t, validTo, "old row must have valid_to set")
}

func TestGetAsset_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetAsset(t.Context(), uuid.New())
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestUpsertRelationship_Idempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

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

func TestUpsertAssetBatch_ReDelivery(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()
	ts := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "batch-redelivery-001",
		LastUpdatedAtSource: &ts,
	}

	results1, err := s.UpsertAssetBatch(ctx, []store.AssetInput{in})
	require.NoError(t, err)
	require.Len(t, results1, 1)

	results2, err := s.UpsertAssetBatch(ctx, []store.AssetInput{in})
	require.NoError(t, err)
	require.Len(t, results2, 1)
	assert.Equal(t, results1[0].ID, results2[0].ID, "re-delivery must return same row ID")

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM assets WHERE source_system=$1 AND source_id=$2 AND valid_to IS NULL",
		"cmdb", "batch-redelivery-001").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "re-delivery must not create a second current row")
}

func TestUpsertAssetBatch_AdvancingTimestamp(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()
	ts1 := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	ts2 := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "batch-advance-001",
		LastUpdatedAtSource: &ts1,
	}
	results1, err := s.UpsertAssetBatch(ctx, []store.AssetInput{in})
	require.NoError(t, err)
	require.Len(t, results1, 1)
	assert.Equal(t, "created", results1[0].Outcome)

	in.LastUpdatedAtSource = &ts2
	results2, err := s.UpsertAssetBatch(ctx, []store.AssetInput{in})
	require.NoError(t, err)
	require.Len(t, results2, 1)
	assert.NotEqual(t, results1[0].ID, results2[0].ID, "advancing timestamp must create a new row")

	var validTo *time.Time
	err = testPool.QueryRow(ctx,
		"SELECT valid_to FROM assets WHERE id=$1", results1[0].ID).Scan(&validTo)
	require.NoError(t, err)
	assert.NotNil(t, validTo, "old row must be expired after timestamp advance")
}

func TestUpsertAssetBatch_Mixed(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()
	ts := time.Now().UTC().Truncate(time.Microsecond)

	existing := store.AssetInput{
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "batch-mixed-existing",
		LastUpdatedAtSource: &ts,
	}
	results0, err := s.UpsertAssetBatch(ctx, []store.AssetInput{existing})
	require.NoError(t, err)
	existingID := results0[0].ID

	newItem := store.AssetInput{
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "batch-mixed-new",
	}
	results, err := s.UpsertAssetBatch(ctx, []store.AssetInput{existing, newItem})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// First item is a re-delivery — same ID, outcome "updated" (dedup path).
	assert.Equal(t, existingID, results[0].ID)
	assert.Equal(t, "updated", results[0].Outcome)

	// Second item is brand-new.
	assert.NotEqual(t, existingID, results[1].ID)
	assert.Equal(t, "created", results[1].Outcome)
}

func TestUpsertRelationshipBatch_HappyPath(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-src-001"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-tgt-001"})
	require.NoError(t, err)

	rels, err := s.UpsertRelationshipBatch(ctx, []store.RelationshipInput{
		{SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID, ArtifactType: "connects", SourceSystem: "cmdb"},
	})
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, aSrc.ID, rels[0].SourceAssetID)
	assert.Equal(t, aTgt.ID, rels[0].TargetAssetID)
	assert.Equal(t, "connects", rels[0].ArtifactType)
}

func TestUpsertRelationshipBatch_Idempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-idem-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-idem-tgt"})
	require.NoError(t, err)

	in := []store.RelationshipInput{
		{SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID, ArtifactType: "connects", SourceSystem: "cmdb"},
	}
	rels1, err := s.UpsertRelationshipBatch(ctx, in)
	require.NoError(t, err)
	require.Len(t, rels1, 1)

	rels2, err := s.UpsertRelationshipBatch(ctx, in)
	require.NoError(t, err)
	require.Len(t, rels2, 1)
	assert.Equal(t, rels1[0].ID, rels2[0].ID, "duplicate batch must return existing relationship")

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_asset_id=$1 AND target_asset_id=$2 AND artifact_type=$3",
		aSrc.ID, aTgt.ID, "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "ON CONFLICT must not insert a duplicate row")
}

func TestUpsertAsset_NilTimestamp_ReDeliveryReVersions(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	// Both calls omit LastUpdatedAtSource (nil). The dedup guard requires both sides
	// to be non-nil, so it is skipped. The second call expires the first row and
	// inserts a new one — this test documents that behaviour explicitly.
	in := store.AssetInput{ArtifactType: "server", SourceSystem: "cmdb", SourceID: "nil-ts-001"}

	a1, created1, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.True(t, created1)

	a2, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.NotEqual(t, a1.ID, a2.ID, "nil-timestamp re-delivery creates a new version (guard is skipped)")

	// Original row must be expired.
	var validTo *time.Time
	err = testPool.QueryRow(ctx, "SELECT valid_to FROM assets WHERE id=$1", a1.ID).Scan(&validTo)
	require.NoError(t, err)
	assert.NotNil(t, validTo, "original nil-timestamp row must be expired on re-delivery")
}

func TestFilterRelationships_ByType(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

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
