// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package equivtest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/inventory-store/internal/inventory/adapters"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// readers builds a DAO-backed and a repo-backed StoreReader over the same pool, so a read
// issued to each must return identical results if the two paths are equivalent.
func readers(t *testing.T) (dao, repo *adapters.StoreReader, s *store.Store) {
	t.Helper()
	s = store.NewFromPool(testPool)
	repoImpl := adapters.NewPostgresAssetRepository(testPool)
	dao = adapters.NewStoreReader(s, repoImpl, adapters.ReadPathDAO)
	repo = adapters.NewStoreReader(s, repoImpl, adapters.ReadPathRepo)
	return dao, repo, s
}

func seed(t *testing.T, s *store.Store, in store.AssetInput) store.Asset {
	t.Helper()
	a, _, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)
	return a
}

// TestGetAsset_DAOvsRepo_Equivalent proves GetAsset returns byte-identical results from both
// paths across a representative spread of nullable fields.
func TestGetAsset_DAOvsRepo_Equivalent(t *testing.T) {
	cleanAssets(t)
	dao, repo, s := readers(t)
	ctx := t.Context()

	ts := time.Now().UTC().Truncate(time.Microsecond)
	cases := []store.AssetInput{
		{IRI: "urn:ocg:asset/cmdb/full-001", Name: "Full", ArtifactType: "server", SourceSystem: "cmdb", SourceID: "full-001",
			Extensions: json.RawMessage(`{"region":"eu"}`), IngestedAt: &ts, CreatedAtSource: &ts, LastUpdatedAtSource: &ts},
		// Minimal: name empty (-> NULL), no timestamps, no extensions.
		{IRI: "urn:ocg:asset/cmdb/min-002", ArtifactType: "db", SourceSystem: "cmdb", SourceID: "min-002"},
	}

	for _, in := range cases {
		seeded := seed(t, s, in)

		fromDAO, errDAO := dao.GetAsset(ctx, seeded.ID)
		fromRepo, errRepo := repo.GetAsset(ctx, seeded.ID)

		require.NoError(t, errDAO)
		require.NoError(t, errRepo)
		assert.Equal(t, fromDAO, fromRepo, "GetAsset mismatch for source_id=%s", in.SourceID)
	}
}

// TestGetAsset_NotFound_Equivalent proves both paths surface the same not-found error.
func TestGetAsset_NotFound_Equivalent(t *testing.T) {
	cleanAssets(t)
	dao, repo, _ := readers(t)
	ctx := t.Context()

	missing := seedMissingID(t)
	_, errDAO := dao.GetAsset(ctx, missing)
	_, errRepo := repo.GetAsset(ctx, missing)

	require.ErrorIs(t, errDAO, store.ErrAssetNotFound)
	require.ErrorIs(t, errRepo, store.ErrAssetNotFound)
}

// TestListAssets_DAOvsRepo_Equivalent proves a paged, filtered list returns identical items
// and an identical next cursor from both paths.
func TestListAssets_DAOvsRepo_Equivalent(t *testing.T) {
	cleanAssets(t)
	dao, repo, s := readers(t)
	ctx := t.Context()

	// Seed enough rows of the same type to force a cursor (limit below count).
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		seed(t, s, store.AssetInput{
			IRI: "urn:ocg:asset/cmdb/list-" + id, Name: "n-" + id,
			ArtifactType: "server", SourceSystem: "cmdb", SourceID: "list-" + id,
		})
	}

	const limit = 2
	daoItems, daoCursor, errDAO := dao.ListAssets(ctx, "server", "cmdb", "", limit, nil)
	repoItems, repoCursor, errRepo := repo.ListAssets(ctx, "server", "cmdb", "", limit, nil)

	require.NoError(t, errDAO)
	require.NoError(t, errRepo)
	assert.Equal(t, daoItems, repoItems, "first page items mismatch")
	assert.Equal(t, daoCursor, repoCursor, "first page cursor mismatch")
	require.NotNil(t, daoCursor, "expected a next cursor with limit < count")

	// Second page via the cursor must also match.
	daoP2, daoC2, err := dao.ListAssets(ctx, "server", "cmdb", "", limit, daoCursor)
	require.NoError(t, err)
	repoP2, repoC2, err := repo.ListAssets(ctx, "server", "cmdb", "", limit, repoCursor)
	require.NoError(t, err)
	assert.Equal(t, daoP2, repoP2, "second page items mismatch")
	assert.Equal(t, daoC2, repoC2, "second page cursor mismatch")
}
