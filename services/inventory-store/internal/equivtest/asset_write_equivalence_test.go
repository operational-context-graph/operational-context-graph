// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package equivtest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/inventory-store/internal/inventory"
	"github.com/operational-context-graph/inventory-store/internal/inventory/adapters"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// This file proves the new repository WRITE path produces the same stored rows as the legacy store
// (DAO) write path. Both paths run against the same database but on disjoint natural keys (a "-dao"
// and a "-repo" suffix), so an identical delivery sequence must yield identical versioned rows.
//
// Read-back goes through the legacy store for both keys, so only the WRITE path differs between the
// two sides — the thing under test. Comparison ignores fields that MUST differ (id, source_id
// suffix) or are wall-clock (created_at); everything temporal/content is asserted equal.

// writeInput is one logical delivery, independent of natural key. applyLegacy/applyRepo stamp the
// key just before writing so the two paths get identical data under different source_ids.
type writeInput struct {
	iri                 string
	name                string
	artifactType        string
	extensions          json.RawMessage
	ingestedAt          *time.Time
	createdAtSource     *time.Time
	lastUpdatedAtSource *time.Time
}

func (w writeInput) legacyInput(sourceID string) store.AssetInput {
	return store.AssetInput{
		IRI: w.iri, Name: w.name, ArtifactType: w.artifactType,
		SourceSystem: "cmdb", SourceID: sourceID, Extensions: w.extensions,
		IngestedAt: w.ingestedAt, CreatedAtSource: w.createdAtSource, LastUpdatedAtSource: w.lastUpdatedAtSource,
	}
}

func (w writeInput) update(sourceID string) inventory.Update {
	return inventory.Update{
		Key:          inventory.NaturalKey{SourceSystem: "cmdb", SourceID: sourceID},
		IRI:          ptrOrNil(w.iri),
		Name:         ptrOrNil(w.name),
		ArtifactType: w.artifactType,
		Extensions:   w.extensions,
		IngestedAt:   w.ingestedAt, CreatedAtSource: w.createdAtSource, LastUpdatedAtSource: w.lastUpdatedAtSource,
	}
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// writeFixture bundles the two paths and the common reader used to read rows back for comparison.
type writeFixture struct {
	s    *store.Store
	repo *adapters.PostgresAssetRepository
	rdr  *adapters.StoreReader // DAO-backed read; identical for both keys so only writes differ
	ctx  context.Context
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	s := store.NewFromPool(testPool)
	repo := adapters.NewPostgresAssetRepository(testPool)
	return writeFixture{
		s:    s,
		repo: repo,
		rdr:  adapters.NewStoreReader(s, repo, adapters.ReadPathDAO),
		ctx:  t.Context(),
	}
}

// normalize zeroes the fields that legitimately differ between the two keys so the rest can be
// compared with assert.Equal: id (random), source_id (the -dao/-repo suffix), created_at (insert
// wall-clock). The versioned content and the validity window are left intact.
func normalize(a store.Asset) store.Asset {
	a.ID = uuid.Nil
	a.SourceID = ""
	a.CreatedAt = time.Time{}
	return a
}

// assertCurrentEquivalent reads the current (open) version of both keys and asserts equivalence.
func assertCurrentEquivalent(t *testing.T, f writeFixture, daoID, repoID uuid.UUID) {
	t.Helper()
	fromDAO, err := f.rdr.GetAsset(f.ctx, daoID)
	require.NoError(t, err)
	fromRepo, err := f.rdr.GetAsset(f.ctx, repoID)
	require.NoError(t, err)
	assert.Equal(t, normalize(fromDAO), normalize(fromRepo), "current version differs between legacy and repo write paths")
}

// TestWrite_FirstDelivery_Equivalent: a brand-new asset. Both paths report created, both store the
// same row.
func TestWrite_FirstDelivery_Equivalent(t *testing.T) {
	cleanAssets(t)
	f := newWriteFixture(t)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := writeInput{
		iri: "urn:ocg:asset/cmdb/new", name: "New", artifactType: "server",
		extensions: json.RawMessage(`{"region":"eu"}`), ingestedAt: &ts, createdAtSource: &ts, lastUpdatedAtSource: &ts,
	}

	daoAsset, daoCreated, err := f.s.UpsertAsset(f.ctx, in.legacyInput("fd-dao"))
	require.NoError(t, err)
	repoAsset, repoCreated, err := f.repo.Upsert(f.ctx, in.update("fd-repo"))
	require.NoError(t, err)

	assert.True(t, daoCreated, "legacy should report created")
	assert.True(t, repoCreated, "repo should report created")
	require.NotNil(t, repoAsset)
	assertCurrentEquivalent(t, f, daoAsset.ID, repoAsset.ID())
}

// TestWrite_ExactRedelivery_Equivalent: the same delivery twice (identical last_updated_at_source)
// must not create a second version on either path.
func TestWrite_ExactRedelivery_Equivalent(t *testing.T) {
	cleanAssets(t)
	f := newWriteFixture(t)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	in := writeInput{
		iri: "urn:ocg:asset/cmdb/redelivery", name: "Redelivery", artifactType: "server",
		lastUpdatedAtSource: &ts,
	}

	// First delivery.
	daoAsset, _, err := f.s.UpsertAsset(f.ctx, in.legacyInput("rd-dao"))
	require.NoError(t, err)
	repoAsset, _, err := f.repo.Upsert(f.ctx, in.update("rd-repo"))
	require.NoError(t, err)

	// Exact re-delivery: no new version, created=false on both.
	daoAsset2, daoCreated, err := f.s.UpsertAsset(f.ctx, in.legacyInput("rd-dao"))
	require.NoError(t, err)
	repoAsset2, repoCreated, err := f.repo.Upsert(f.ctx, in.update("rd-repo"))
	require.NoError(t, err)

	assert.False(t, daoCreated, "legacy re-delivery must not report created")
	assert.False(t, repoCreated, "repo re-delivery must not report created")
	// Re-delivery returns the unchanged current version: same id as the first delivery.
	assert.Equal(t, daoAsset.ID, daoAsset2.ID, "legacy re-delivery must return the same version")
	assert.Equal(t, repoAsset.ID(), repoAsset2.ID(), "repo re-delivery must return the same version")

	assertCurrentEquivalent(t, f, daoAsset.ID, repoAsset.ID())
	assertSingleOpenVersion(t, f, "rd-dao")
	assertSingleOpenVersion(t, f, "rd-repo")
}

// TestWrite_AdvancingTimestamp_Equivalent: a newer last_updated_at_source expires the old version
// at the boundary and opens a new one. Boundaries must be contiguous and identical across paths.
func TestWrite_AdvancingTimestamp_Equivalent(t *testing.T) {
	cleanAssets(t)
	f := newWriteFixture(t)

	t0 := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	t1 := t0.Add(time.Hour)
	first := writeInput{iri: "urn:ocg:asset/cmdb/adv", name: "v1", artifactType: "server", lastUpdatedAtSource: &t0}
	second := writeInput{iri: "urn:ocg:asset/cmdb/adv", name: "v2", artifactType: "server", lastUpdatedAtSource: &t1}

	_, _, err := f.s.UpsertAsset(f.ctx, first.legacyInput("adv-dao"))
	require.NoError(t, err)
	_, _, err = f.repo.Upsert(f.ctx, first.update("adv-repo"))
	require.NoError(t, err)

	daoAsset, daoCreated, err := f.s.UpsertAsset(f.ctx, second.legacyInput("adv-dao"))
	require.NoError(t, err)
	repoAsset, repoCreated, err := f.repo.Upsert(f.ctx, second.update("adv-repo"))
	require.NoError(t, err)

	assert.False(t, daoCreated, "legacy update of existing asset is not created")
	assert.False(t, repoCreated, "repo update of existing asset is not created")

	assertCurrentEquivalent(t, f, daoAsset.ID, repoAsset.ID())
	// Exactly one open version per key; expired version closes exactly at the new open's valid_from.
	assertSingleOpenVersion(t, f, "adv-dao")
	assertSingleOpenVersion(t, f, "adv-repo")
	assertContiguousBoundary(t, f, "adv-dao")
	assertContiguousBoundary(t, f, "adv-repo")
}

// TestWrite_NilTimestamp_Equivalent: with no last_updated_at_source the boundary falls back to
// NOW(). We cannot assert exact instants, but both paths must still produce one open version whose
// window is contiguous with the expired one.
func TestWrite_NilTimestamp_Equivalent(t *testing.T) {
	cleanAssets(t)
	f := newWriteFixture(t)

	v1 := writeInput{iri: "urn:ocg:asset/cmdb/nilts", name: "v1", artifactType: "server"}
	v2 := writeInput{iri: "urn:ocg:asset/cmdb/nilts", name: "v2", artifactType: "server"}

	_, _, err := f.s.UpsertAsset(f.ctx, v1.legacyInput("nil-dao"))
	require.NoError(t, err)
	_, _, err = f.repo.Upsert(f.ctx, v1.update("nil-repo"))
	require.NoError(t, err)

	daoAsset, _, err := f.s.UpsertAsset(f.ctx, v2.legacyInput("nil-dao"))
	require.NoError(t, err)
	repoAsset, _, err := f.repo.Upsert(f.ctx, v2.update("nil-repo"))
	require.NoError(t, err)

	// Content (name, iri, extensions, null timestamps) must match; validity instants are NOW()-based.
	dao, err := f.rdr.GetAsset(f.ctx, daoAsset.ID)
	require.NoError(t, err)
	repo, err := f.rdr.GetAsset(f.ctx, repoAsset.ID())
	require.NoError(t, err)
	dn, rn := normalize(dao), normalize(repo)
	dn.ValidFrom, rn.ValidFrom = time.Time{}, time.Time{} // NOW()-based, cannot be equal across two writes
	assert.Equal(t, dn, rn, "null-timestamp content differs between paths")

	assertSingleOpenVersion(t, f, "nil-dao")
	assertSingleOpenVersion(t, f, "nil-repo")
	assertContiguousBoundary(t, f, "nil-dao")
	assertContiguousBoundary(t, f, "nil-repo")
}

// TestWriteBatch_MixedOutcomes_Equivalent: a batch of new + re-delivery + update. The legacy string
// outcomes must line up with the repo's structured Created/Redelivery flags, and the resulting
// current rows must match.
func TestWriteBatch_MixedOutcomes_Equivalent(t *testing.T) {
	cleanAssets(t)
	f := newWriteFixture(t)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	tsNew := ts.Add(time.Hour)

	// Seed one asset on each path so the batch can re-deliver and update it.
	seedUnchanged := writeInput{iri: "urn:ocg:asset/cmdb/b-keep", name: "keep", artifactType: "server", lastUpdatedAtSource: &ts}
	seedUpdate := writeInput{iri: "urn:ocg:asset/cmdb/b-upd", name: "v1", artifactType: "server", lastUpdatedAtSource: &ts}
	// DAO seeds.
	_, _, err := f.s.UpsertAsset(f.ctx, seedUnchanged.legacyInput("b-keep-dao"))
	require.NoError(t, err)
	_, _, err = f.s.UpsertAsset(f.ctx, seedUpdate.legacyInput("b-upd-dao"))
	require.NoError(t, err)
	// Repo seeds.
	_, _, err = f.repo.Upsert(f.ctx, seedUnchanged.update("b-keep-repo"))
	require.NoError(t, err)
	_, _, err = f.repo.Upsert(f.ctx, seedUpdate.update("b-upd-repo"))
	require.NoError(t, err)

	// Batch: re-deliver b-keep (unchanged), update b-upd (new ts), create b-fresh.
	reKeep := seedUnchanged
	doUpdate := writeInput{iri: "urn:ocg:asset/cmdb/b-upd", name: "v2", artifactType: "server", lastUpdatedAtSource: &tsNew}
	fresh := writeInput{iri: "urn:ocg:asset/cmdb/b-fresh", name: "fresh", artifactType: "server", lastUpdatedAtSource: &ts}

	daoBatch := []store.AssetInput{
		reKeep.legacyInput("b-keep-dao"), doUpdate.legacyInput("b-upd-dao"), fresh.legacyInput("b-fresh-dao"),
	}
	repoBatch := []inventory.Update{
		reKeep.update("b-keep-repo"), doUpdate.update("b-upd-repo"), fresh.update("b-fresh-repo"),
	}

	daoResults, err := f.s.UpsertAssetBatch(f.ctx, daoBatch)
	require.NoError(t, err)
	repoResults, err := f.repo.UpsertBatch(f.ctx, repoBatch)
	require.NoError(t, err)
	require.Len(t, daoResults, 3)
	require.Len(t, repoResults, 3)

	// Outcomes line up element-for-element.
	wantOutcomes := []string{"unchanged", "updated", "created"}
	for i, want := range wantOutcomes {
		assert.Equal(t, want, daoResults[i].Outcome, "legacy outcome[%d]", i)
		assert.Equal(t, want, repoOutcome(repoResults[i]), "repo outcome[%d]", i)
	}

	// Resulting current rows match per key.
	for _, base := range []string{"b-keep", "b-upd", "b-fresh"} {
		daoCur := currentID(t, f, base+"-dao")
		repoCur := currentID(t, f, base+"-repo")
		assertCurrentEquivalent(t, f, daoCur, repoCur)
		assertSingleOpenVersion(t, f, base+"-dao")
		assertSingleOpenVersion(t, f, base+"-repo")
	}
}

// repoOutcome maps a structured repo result to the legacy outcome vocabulary for comparison.
func repoOutcome(r inventory.UpsertResult) string {
	switch {
	case r.Redelivery:
		return "unchanged"
	case r.Created:
		return "created"
	default:
		return "updated"
	}
}

// --- temporal-invariant helpers (query the DB directly) ---

// assertSingleOpenVersion asserts exactly one row with valid_to IS NULL exists for the key.
func assertSingleOpenVersion(t *testing.T, f writeFixture, sourceID string) {
	t.Helper()
	var n int
	err := testPool.QueryRow(f.ctx,
		`SELECT count(*) FROM assets WHERE source_system = 'cmdb' AND source_id = $1 AND valid_to IS NULL`,
		sourceID).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "expected exactly one open version for source_id=%s", sourceID)
}

// assertContiguousBoundary asserts the most-recently-expired version closes exactly when the open
// version opens: max(valid_to) over expired rows == valid_from of the open row.
func assertContiguousBoundary(t *testing.T, f writeFixture, sourceID string) {
	t.Helper()
	var expiredTo, openFrom time.Time
	err := testPool.QueryRow(f.ctx,
		`SELECT max(valid_to) FROM assets WHERE source_system = 'cmdb' AND source_id = $1 AND valid_to IS NOT NULL`,
		sourceID).Scan(&expiredTo)
	require.NoError(t, err)
	err = testPool.QueryRow(f.ctx,
		`SELECT valid_from FROM assets WHERE source_system = 'cmdb' AND source_id = $1 AND valid_to IS NULL`,
		sourceID).Scan(&openFrom)
	require.NoError(t, err)
	assert.True(t, expiredTo.Equal(openFrom),
		"expired valid_to (%s) must equal open valid_from (%s) for source_id=%s", expiredTo, openFrom, sourceID)
}

// currentID returns the id of the open version for a key.
func currentID(t *testing.T, f writeFixture, sourceID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testPool.QueryRow(f.ctx,
		`SELECT id FROM assets WHERE source_system = 'cmdb' AND source_id = $1 AND valid_to IS NULL`,
		sourceID).Scan(&id)
	require.NoError(t, err)
	return id
}
