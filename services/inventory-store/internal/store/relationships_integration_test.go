// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"testing"

	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	// Second call with same natural key must return 200 OK — silently drop, return existing row.
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
