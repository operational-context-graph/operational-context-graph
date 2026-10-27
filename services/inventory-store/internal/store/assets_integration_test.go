// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/apperr"
	"github.com/operational-context-graph/operational-context-graph/services/inventory-store/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertAsset_Created(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	in := store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/srv-001",
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
		IRI:                 "urn:ocg:asset/cmdb/srv-002",
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
		IRI:                 "urn:ocg:asset/cmdb/srv-003",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "srv-003",
		LastUpdatedAtSource: &ts,
	}
	a1, created1, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	require.True(t, created1)

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
		IRI:                 "urn:ocg:asset/cmdb/srv-004",
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

func TestUpsertAsset_NilTimestamp_ReDeliveryReVersions(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	// Both calls omit LastUpdatedAtSource (nil). The dedup guard requires both sides
	// to be non-nil, so it is skipped. The second call expires the first row and
	// inserts a new one — this test documents that behaviour explicitly.
	in := store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/nil-ts-001",
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "nil-ts-001",
	}

	a1, created1, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.True(t, created1)

	a2, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.NotEqual(t, a1.ID, a2.ID, "nil-timestamp re-delivery creates a new version (guard is skipped)")

	var validTo *time.Time
	err = testPool.QueryRow(ctx, "SELECT valid_to FROM assets WHERE id=$1", a1.ID).Scan(&validTo)
	require.NoError(t, err)
	assert.NotNil(t, validTo, "original nil-timestamp row must be expired on re-delivery")
}

func TestUpsertAssetBatch_ReDelivery(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()
	ts := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		IRI:                 "urn:ocg:asset/cmdb/batch-redelivery-001",
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
		IRI:                 "urn:ocg:asset/cmdb/batch-advance-001",
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
		IRI:                 "urn:ocg:asset/cmdb/batch-mixed-existing",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "batch-mixed-existing",
		LastUpdatedAtSource: &ts,
	}
	results0, err := s.UpsertAssetBatch(ctx, []store.AssetInput{existing})
	require.NoError(t, err)
	existingID := results0[0].ID

	newItem := store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/batch-mixed-new",
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "batch-mixed-new",
	}
	results, err := s.UpsertAssetBatch(ctx, []store.AssetInput{existing, newItem})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// First item is a re-delivery — same ID, outcome "unchanged" (dedup guard fires).
	assert.Equal(t, existingID, results[0].ID)
	assert.Equal(t, "unchanged", results[0].Outcome)

	// Second item is brand-new.
	assert.NotEqual(t, existingID, results[1].ID)
	assert.Equal(t, "created", results[1].Outcome)
}

func TestUpsertAsset_IRIAndIngestedAt_RoundTrip(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/iri-roundtrip-001",
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "iri-roundtrip-001",
		IngestedAt:   &ts,
	}
	a, created, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.True(t, created)

	got, err := s.GetAsset(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.IRI)
	assert.Equal(t, "urn:ocg:asset/cmdb/iri-roundtrip-001", *got.IRI)
	require.NotNil(t, got.IngestedAt)
	assert.True(t, ts.Equal(*got.IngestedAt), "ingested_at must survive insert unchanged")
}

func TestUpsertAsset_ValidFromEqualsLastUpdatedAtSource(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := store.AssetInput{
		IRI:                 "urn:ocg:asset/cmdb/valid-from-001",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "valid-from-001",
		LastUpdatedAtSource: &ts,
	}
	a, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)
	assert.True(t, ts.Equal(a.ValidFrom),
		"valid_from must equal last_updated_at_source when present (got %v, want %v)", a.ValidFrom, ts)
}

func TestUpsertAsset_ExpiredValidToMatchesNewValidFrom(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	ts1 := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond)
	ts2 := time.Now().UTC().Truncate(time.Microsecond)

	in := store.AssetInput{
		IRI:                 "urn:ocg:asset/cmdb/valid-boundary-001",
		ArtifactType:        "server",
		SourceSystem:        "cmdb",
		SourceID:            "valid-boundary-001",
		LastUpdatedAtSource: &ts1,
	}
	a1, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)

	in.LastUpdatedAtSource = &ts2
	a2, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)

	var validTo *time.Time
	err = testPool.QueryRow(ctx, "SELECT valid_to FROM assets WHERE id=$1", a1.ID).Scan(&validTo)
	require.NoError(t, err)
	require.NotNil(t, validTo, "old row must be expired")
	assert.True(t, validTo.Equal(a2.ValidFrom),
		"expired row valid_to must equal new row valid_from (got %v, want %v)", validTo, a2.ValidFrom)
}

func TestUpsertAsset_ExtensionsDefaultsToEmptyObject(t *testing.T) {
	cleanTables(t)
	s := newStore(t)
	ctx := t.Context()

	in := store.AssetInput{
		IRI:          "urn:ocg:asset/cmdb/ext-default-001",
		ArtifactType: "server",
		SourceSystem: "cmdb",
		SourceID:     "ext-default-001",
		// Extensions deliberately omitted
	}
	a, _, err := s.UpsertAsset(ctx, in)
	require.NoError(t, err)

	got, err := s.GetAsset(ctx, a.ID)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(got.Extensions, &m)
	require.NoError(t, err, "extensions must be valid JSON")
	assert.Empty(t, m, "extensions must default to {} when not provided")
}
