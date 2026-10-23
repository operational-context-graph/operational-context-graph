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

	in := store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/src-rel-001_connects_tgt-rel-001",
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceID:      "src-rel-001_connects_tgt-rel-001",
		SourceKey:     "cmdb:src-rel-001",
		TargetKey:     "cmdb:tgt-rel-001",
	}
	r1, created1, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.True(t, created1)

	// Second call with same logical key — silently drop, return existing row.
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
			IRI:           "urn:ocg:rel/cmdb/relb-src-001_connects_relb-tgt-001",
			SourceAssetID: aSrc.ID,
			TargetAssetID: aTgt.ID,
			ArtifactType:  "connects",
			SourceSystem:  "cmdb",
			SourceID:      "relb-src-001_connects_relb-tgt-001",
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

	in := []store.RelationshipInput{
		{
			IRI:           "urn:ocg:rel/cmdb/relb-idem-src_connects_relb-idem-tgt",
			SourceAssetID: aSrc.ID,
			TargetAssetID: aTgt.ID,
			ArtifactType:  "connects",
			SourceSystem:  "cmdb",
			SourceID:      "relb-idem-src_connects_relb-idem-tgt",
			SourceKey:     "cmdb:relb-idem-src",
			TargetKey:     "cmdb:relb-idem-tgt",
		},
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
		"SELECT COUNT(*) FROM relationships WHERE source_key=$1 AND target_key=$2 AND artifact_type=$3 AND valid_to IS NULL",
		"cmdb:relb-idem-src", "cmdb:relb-idem-tgt", "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "ON CONFLICT must not insert a duplicate row")
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
		IRI:           "urn:ocg:rel/cmdb/src-filter-001_connects_tgt-filter-001",
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceID:  "src-filter-001_connects_tgt-filter-001",
		SourceKey: "cmdb:src-filter-001", TargetKey: "cmdb:tgt-filter-001",
	})
	require.NoError(t, err)
	_, _, err = s.UpsertRelationship(ctx, store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/src-filter-001_depends_on_tgt-filter-001",
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "depends_on", SourceSystem: "cmdb",
		SourceID:  "src-filter-001_depends_on_tgt-filter-001",
		SourceKey: "cmdb:src-filter-001", TargetKey: "cmdb:tgt-filter-001",
	})
	require.NoError(t, err)

	artifactType := "connects"
	results, err := s.FilterRelationships(ctx, nil, nil, nil, artifactType, 100)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "connects", results[0].ArtifactType)
}

func TestUpsertRelationship_IRIAndSourceID_RoundTrip(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/iri-rel-src", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "iri-rel-src"})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{IRI: "urn:ocg:asset/cmdb/iri-rel-tgt", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "iri-rel-tgt"})
	require.NoError(t, err)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/iri-rel-src_connects_iri-rel-tgt",
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceID:      "iri-rel-src_connects_iri-rel-tgt",
		SourceKey:     "cmdb:iri-rel-src",
		TargetKey:     "cmdb:iri-rel-tgt",
		IngestedAt:    &ts,
	}
	r, created, err := s.UpsertRelationship(ctx, in)
	require.NoError(t, err)
	assert.True(t, created)

	got, err := s.GetRelationshipByID(ctx, r.ID)
	require.NoError(t, err)

	require.NotNil(t, got.IRI)
	assert.Equal(t, "urn:ocg:rel/cmdb/iri-rel-src_connects_iri-rel-tgt", *got.IRI)
	require.NotNil(t, got.SourceID)
	assert.Equal(t, "iri-rel-src_connects_iri-rel-tgt", *got.SourceID)
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
		IRI:           "urn:ocg:rel/cmdb/ext-rel-src_connects_ext-rel-tgt",
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceID:      "ext-rel-src_connects_ext-rel-tgt",
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

func TestUpsertRelationship_LogicalKeyDedup_SurvivesAssetReversion(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	// Insert initial asset versions.
	ts1 := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	aSrc, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI:                 "urn:ocg:asset/cmdb/reversion-src",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "reversion-src",
		LastUpdatedAtSource: &ts1,
	})
	require.NoError(t, err)
	aTgt, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/reversion-tgt",
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "reversion-tgt",
	})
	require.NoError(t, err)

	// Create edge with stable logical keys.
	r1, created1, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/reversion-src_connects_reversion-tgt",
		SourceAssetID: aSrc.ID,
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceID:      "reversion-src_connects_reversion-tgt",
		SourceKey:     "cmdb:reversion-src",
		TargetKey:     "cmdb:reversion-tgt",
	})
	require.NoError(t, err)
	assert.True(t, created1)

	// Re-version the source asset (new UUID, same source_system:source_id).
	ts2 := time.Now().UTC().Truncate(time.Microsecond)
	aSrc2, _, err := s.UpsertAsset(ctx, store.AssetInput{
		IRI:                 "urn:ocg:asset/cmdb/reversion-src",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "reversion-src",
		LastUpdatedAtSource: &ts2,
	})
	require.NoError(t, err)
	assert.NotEqual(t, aSrc.ID, aSrc2.ID, "new version must have a different UUID")

	// Upsert same logical edge with the new source UUID. Because source_key + target_key
	// still match the open row, ON CONFLICT fires and the edge is NOT duplicated.
	r2, created2, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/reversion-src_connects_reversion-tgt",
		SourceAssetID: aSrc2.ID, // new UUID
		TargetAssetID: aTgt.ID,
		ArtifactType:  "connects",
		SourceSystem:  "cmdb",
		SourceID:      "reversion-src_connects_reversion-tgt",
		SourceKey:     "cmdb:reversion-src", // same logical key
		TargetKey:     "cmdb:reversion-tgt",
	})
	require.NoError(t, err)
	assert.False(t, created2, "same logical key must not create a new edge after asset re-version")
	assert.Equal(t, r1.ID, r2.ID, "returned edge ID must be the existing one")

	// Exactly one open edge in the database.
	var count int
	err = testPool.QueryRow(ctx,
		"SELECT COUNT(*) FROM relationships WHERE source_key=$1 AND target_key=$2 AND artifact_type=$3 AND valid_to IS NULL",
		"cmdb:reversion-src", "cmdb:reversion-tgt", "connects").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
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
		IRI:           "urn:ocg:rel/cmdb/curr-src_connects_curr-tgt",
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceID:  "curr-src_connects_curr-tgt",
		SourceKey: "cmdb:curr-src", TargetKey: "cmdb:curr-tgt",
	})
	require.NoError(t, err)

	// Insert and immediately close edge: src → tgt2.
	closedEdge, _, err := s.UpsertRelationship(ctx, store.RelationshipInput{
		IRI:           "urn:ocg:rel/cmdb/curr-src_connects_curr-tgt2",
		SourceAssetID: aSrc.ID, TargetAssetID: aTgt2.ID,
		ArtifactType: "connects", SourceSystem: "cmdb",
		SourceID:  "curr-src_connects_curr-tgt2",
		SourceKey: "cmdb:curr-src", TargetKey: "cmdb:curr-tgt2",
	})
	require.NoError(t, err)
	// Close the edge directly via SQL (CloseRelationship is not yet exposed).
	_, err = testPool.Exec(ctx, `UPDATE relationships SET valid_to = NOW() WHERE id = $1`, closedEdge.ID)
	require.NoError(t, err)

	results, err := s.FilterRelationships(ctx, nil, nil, nil, "connects", 100)
	require.NoError(t, err)
	require.Len(t, results, 1, "filter must return only open edges")
	assert.Equal(t, openEdge.ID, results[0].ID)
}
