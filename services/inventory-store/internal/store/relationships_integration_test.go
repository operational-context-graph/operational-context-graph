// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertRelationship_Idempotent(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	src := store.AssetInput{IRI: "urn:ocg:asset/cmdb/src-rel-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "src-rel-001"}
	tgt := store.AssetInput{IRI: "urn:ocg:asset/cmdb/tgt-rel-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "tgt-rel-001"}
	aSrc, _, err := s.UpsertAsset(ctx, src)
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, tgt)
	require.NoError(t, err)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.RelationshipInput{
		SourceAssetID:       aSrc.ID,
		TargetAssetID:       aTgt.ID,
		ArtifactType:        "connects",
		SourceSystem:        "cmdb",
		SourceKey:           "cmdb:src-rel-001",
		TargetKey:           "cmdb:tgt-rel-001",
		LastUpdatedAtSource: &ts,
	}
	r1, created1, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.True(t, created1)

	// Re-delivery with same last_updated_at_source — must return the same row unchanged.
	r2, created2, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, r1.ID, r2.ID)

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_key=$1 AND target_key=$2 AND artifact_type=$3 AND valid_to IS NULL",
		"cmdb:src-rel-001", "cmdb:tgt-rel-001", "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUpsertRelationshipBatch_HappyPath(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/relb-src-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-src-001"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/relb-tgt-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-tgt-001"})
	require.NoError(t, err)

	rels, err := s.UpsertRelationshipBatch(ctx, []store.RelationshipInput{
		{
			SourceAssetID: aSrc.ID,
			TargetAssetID: aTgt.ID,
			ArtifactType:  "connects",
			SourceSystem:  "cmdb",
			SourceKey:     "cmdb:relb-src-001",
			TargetKey:     "cmdb:relb-tgt-001",
		},
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

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/relb-idem-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-idem-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/relb-idem-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "relb-idem-tgt"})
	require.NoError(t, err)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := []store.RelationshipInput{
		{
			SourceAssetID:       aSrc.ID,
			TargetAssetID:       aTgt.ID,
			ArtifactType:        "connects",
			SourceSystem:        "cmdb",
			SourceKey:           "cmdb:relb-idem-src",
			TargetKey:           "cmdb:relb-idem-tgt",
			LastUpdatedAtSource: &ts,
		},
	}
	rels1, err := s.UpsertRelationshipBatch(ctx, in)
	require.NoError(t, err)
	require.Len(t, rels1, 1)

	rels2, err := s.UpsertRelationshipBatch(ctx, in)
	require.NoError(t, err)
	require.Len(t, rels2, 1)
	assert.Equal(t, rels1[0].ID, rels2[0].ID, "re-delivery must return existing relationship")

	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_key=$1 AND target_key=$2 AND artifact_type=$3 AND valid_to IS NULL",
		"cmdb:relb-idem-src", "cmdb:relb-idem-tgt", "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "re-delivery must not insert a new row")
}

func TestFilterRelationships_ByType(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	src := store.AssetInput{IRI: "urn:ocg:asset/cmdb/src-filter-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "src-filter-001"}
	tgt := store.AssetInput{IRI: "urn:ocg:asset/cmdb/tgt-filter-001", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "tgt-filter-001"}
	aSrc, _, err := s.UpsertAsset(ctx, src)
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, tgt)
	require.NoError(t, err)

	_, _, err = s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:src-filter-001", TargetKey: "cmdb:tgt-filter-001",
	})
	require.NoError(t, err)
	_, _, err = s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "depends_on", SourceSystem: "cmdb",
		SourceKey: "cmdb:src-filter-001", TargetKey: "cmdb:tgt-filter-001",
	})
	require.NoError(t, err)

	artifactType := "connects"
	results, err := s.FilterRelationships(ctx, nil, nil, nil, artifactType, 100)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "connects", results[0].ArtifactType)
}

func TestUpsertRelationship_IngestedAtRoundTrip(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/iri-rel-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "iri-rel-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/iri-rel-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "iri-rel-tgt"})
	require.NoError(t, err)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.RelationshipInput{
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceKey:     "cmdb:iri-rel-src",
		TargetKey:     "cmdb:iri-rel-tgt",
		IngestedAt:    &ts,
	}
	r, created, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.True(t, created)

	got, err := s.GetRelationshipByID(ctx, r.ID)
	require.NoError(t, err)

	require.NotNil(t, got.SourceKey)
	assert.Equal(t, "cmdb:iri-rel-src", *got.SourceKey)
	require.NotNil(t, got.TargetKey)
	assert.Equal(t, "cmdb:iri-rel-tgt", *got.TargetKey)
	require.NotNil(t, got.IngestedAt)
	assert.True(t, ts.Equal(*got.IngestedAt), "ingested_at must survive insert unchanged")
	assert.False(t, got.ValidFrom.IsZero(), "valid_from must be set")
	assert.Nil(t, got.ValidTo, "newly inserted edge must be open (valid_to IS NULL)")
}

func TestUpsertRelationship_ExtensionsDefaultsToEmptyObject(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/ext-rel-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "ext-rel-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/ext-rel-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "ext-rel-tgt"})
	require.NoError(t, err)

	in := store.RelationshipInput{
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceKey:     "cmdb:ext-rel-src",
		TargetKey:     "cmdb:ext-rel-tgt",
		// Extensions deliberately omitted
	}
	r, _, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)

	got, err := s.GetRelationshipByID(ctx, r.ID)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(got.Extensions, &m)
	require.NoError(t, err, "extensions must be valid JSON")
	assert.Empty(t, m, "extensions must default to {} when not provided")
}

// TestUpsertRelationship_RePoint_SameTimestamp verifies that re-sending an edge with the same
// last_updated_at_source (re-delivery) returns the existing row unchanged.
func TestUpsertRelationship_RePoint_SameTimestamp(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/repoint-same-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "repoint-same-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/repoint-same-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "repoint-same-tgt"})
	require.NoError(t, err)

	ts := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	r1, created1, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:repoint-same-src", TargetKey: "cmdb:repoint-same-tgt",
		LastUpdatedAtSource: &ts,
	})
	require.NoError(t, err)
	assert.True(t, created1)

	// Re-deliver with the same timestamp — must return the same row unchanged.
	r2, created2, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:repoint-same-src", TargetKey: "cmdb:repoint-same-tgt",
		LastUpdatedAtSource: &ts,
	})
	require.NoError(t, err)
	assert.False(t, created2)
	assert.Equal(t, r1.ID, r2.ID)
}

// TestUpsertRelationship_RePoint_AssetReversion verifies the re-point case: when an asset is
// re-versioned (new UUID, same logical key) and the pipeline re-sends the edge with a new
// last_updated_at_source, the edge UUID and valid_from are preserved and only the endpoint
// UUID and last_updated_at_source are updated in place.
func TestUpsertRelationship_RePoint_AssetReversion(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	assetTs1 := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI: "urn:ocg:asset/cmdb/rp-src", ArtifactType: "server",
		SourceSystem: "cmdb", SourceID: "rp-src", LastUpdatedAtSource: &assetTs1,
	})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI: "urn:ocg:asset/cmdb/rp-tgt", ArtifactType: "server",
		SourceSystem: "cmdb", SourceID: "rp-tgt",
	})
	require.NoError(t, err)

	relTs1 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Microsecond)
	r1, created1, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:rp-src", TargetKey: "cmdb:rp-tgt",
		LastUpdatedAtSource: &relTs1,
	})
	require.NoError(t, err)
	assert.True(t, created1)
	originalValidFrom := r1.ValidFrom

	// Re-version the source asset — new UUID, same logical key.
	assetTs2 := time.Now().UTC().Truncate(time.Microsecond)
	aSrc2, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI: "urn:ocg:asset/cmdb/rp-src", ArtifactType: "server",
		SourceSystem: "cmdb", SourceID: "rp-src", LastUpdatedAtSource: &assetTs2,
	})
	require.NoError(t, err)
	assert.NotEqual(t, aSrc.ID, aSrc2.ID)

	// Re-send the edge with the new asset UUID and an advanced relationship timestamp.
	// Topology has not changed — must re-point, not expire+insert.
	relTs2 := assetTs2
	r2, created2, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc2.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:rp-src", TargetKey: "cmdb:rp-tgt",
		LastUpdatedAtSource: &relTs2,
	})
	require.NoError(t, err)
	assert.False(t, created2, "re-point is not a creation")
	assert.Equal(t, r1.ID, r2.ID, "edge UUID must be preserved on re-point")
	assert.True(t, originalValidFrom.Equal(r2.ValidFrom), "valid_from must be preserved on re-point")
	assert.Nil(t, r2.ValidTo, "edge must remain open after re-point")
	assert.Equal(t, aSrc2.ID, r2.SourceAssetID, "source_asset_id must be updated to new UUID")
	assert.True(t, relTs2.Equal(*r2.LastUpdatedAtSource), "last_updated_at_source must be updated")

	// Exactly one open row — no expire+insert occurred.
	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_key=$1 AND target_key=$2 AND artifact_type=$3",
		"cmdb:rp-src", "cmdb:rp-tgt", "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "re-point must not create a second row")
}

func TestFilterRelationships_OnlyReturnsCurrentEdges(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/curr-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "curr-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/curr-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "curr-tgt"})
	require.NoError(t, err)
	aTgt2, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/curr-tgt2", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "curr-tgt2"})
	require.NoError(t, err)

	// Open edge: src → tgt.
	openEdge, _, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:curr-src", TargetKey: "cmdb:curr-tgt",
	})
	require.NoError(t, err)

	// Insert and immediately close edge: src → tgt2.
	closedEdge, _, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt2.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceKey: "cmdb:curr-src", TargetKey: "cmdb:curr-tgt2",
	})
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `UPDATE relationships SET valid_to = NOW() WHERE id = $1`, closedEdge.ID)
	require.NoError(t, err)

	results, err := s.FilterRelationships(ctx, nil, nil, nil, "connects", 100)
	require.NoError(t, err)
	require.Len(t, results, 1, "filter must return only open edges")
	assert.Equal(t, openEdge.ID, results[0].ID)
}
