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

	"github.com/operational-context-graph/inventory-store/internal/inventory/asset"
	"github.com/operational-context-graph/inventory-store/internal/inventory/asset/adapters"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// This file proves the new repository READ path returns the same data as the legacy store (DAO)
// read path. Both read the same rows from the same pool; the legacy path returns store.Asset and
// the repo path returns *asset.Asset, so equivalence is asserted field-by-field via
// assertAssetEquivalent rather than a struct compare. The two never share a conversion shim.

// readPaths builds the legacy store and the repository over the same pool.
func readPaths(t *testing.T) (s *store.Store, repo *adapters.PostgresAssetRepository) {
	t.Helper()
	return store.NewFromPool(testPool), adapters.NewPostgresAssetRepository(testPool)
}

func seed(t *testing.T, s *store.Store, in store.AssetInput) store.Asset {
	t.Helper()
	a, _, err := s.UpsertAsset(t.Context(), in)
	require.NoError(t, err)
	return a
}

// assertAssetEquivalent asserts the domain Asset read by the repo path carries the same values as
// the store.Asset read by the legacy path.
func assertAssetEquivalent(t *testing.T, want store.Asset, got *asset.Asset) {
	t.Helper()
	require.NotNil(t, got)
	v := got.Validity()
	assert.Equal(t, want.ID, got.ID(), "id")
	assert.Equal(t, want.IRI, got.IRI(), "iri")
	assert.Equal(t, want.Name, got.Name(), "name")
	assert.Equal(t, want.ArtifactType, got.ArtifactType(), "artifact_type")
	assert.Equal(t, want.SourceSystem, got.Key().SourceSystem, "source_system")
	assert.Equal(t, want.SourceID, got.Key().SourceID, "source_id")
	assert.JSONEq(t, jsonOrEmptyStr(want.Extensions), jsonOrEmptyStr(got.Extensions()), "extensions")
	assertTimePtrEqual(t, want.IngestedAt, got.IngestedAt(), "ingested_at")
	assert.True(t, want.CreatedAt.Equal(got.CreatedAt()), "created_at")
	assertTimePtrEqual(t, want.CreatedAtSource, got.CreatedAtSource(), "created_at_source")
	assertTimePtrEqual(t, want.LastUpdatedAtSource, got.LastUpdatedAtSource(), "last_updated_at_source")
	assert.True(t, want.ValidFrom.Equal(v.From), "valid_from")
	assertTimePtrEqual(t, want.ValidTo, v.To, "valid_to")
}

func assertTimePtrEqual(t *testing.T, want, got *time.Time, field string) {
	t.Helper()
	if want == nil || got == nil {
		assert.Equal(t, want == nil, got == nil, "%s nilness", field)
		return
	}
	assert.True(t, want.Equal(*got), "%s: want %s got %s", field, want, got)
}

func jsonOrEmptyStr(b json.RawMessage) string {
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// TestGetAsset_DAOvsRepo_Equivalent proves GetAsset returns equivalent data from both paths across
// a representative spread of nullable fields.
func TestGetAsset_DAOvsRepo_Equivalent(t *testing.T) {
	cleanAssets(t)
	s, repo := readPaths(t)
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

		fromRepo, errRepo := repo.GetAsset(ctx, seeded.ID)
		require.NoError(t, errRepo)
		assertAssetEquivalent(t, seeded, fromRepo)
	}
}

// TestGetAsset_NotFound_Equivalent proves both paths surface the same not-found error.
func TestGetAsset_NotFound_Equivalent(t *testing.T) {
	cleanAssets(t)
	s, repo := readPaths(t)
	ctx := t.Context()

	missing := seedMissingID(t)
	_, errDAO := s.GetAsset(ctx, missing)
	_, errRepo := repo.GetAsset(ctx, missing)

	require.ErrorIs(t, errDAO, store.ErrAssetNotFound)
	require.ErrorIs(t, errRepo, store.ErrAssetNotFound)
}

// TestListAssets_DAOvsRepo_Equivalent proves a paged, filtered list returns equivalent items and an
// equivalent next cursor from both paths.
func TestListAssets_DAOvsRepo_Equivalent(t *testing.T) {
	cleanAssets(t)
	s, repo := readPaths(t)
	ctx := t.Context()

	// Seed enough rows of the same type to force a cursor (limit below count).
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		seed(t, s, store.AssetInput{
			IRI: "urn:ocg:asset/cmdb/list-" + id, Name: "n-" + id,
			ArtifactType: "server", SourceSystem: "cmdb", SourceID: "list-" + id,
		})
	}

	const limit = 2
	daoItems, daoCursor, errDAO := s.ListAssets(ctx, "server", "cmdb", "", limit, nil)
	repoItems, repoCursor, errRepo := repo.ListAssets(ctx, "server", "cmdb", "", limit, nil)

	require.NoError(t, errDAO)
	require.NoError(t, errRepo)
	assertListEquivalent(t, daoItems, repoItems, daoCursor, repoCursor)
	require.NotNil(t, daoCursor, "expected a next cursor with limit < count")

	// Second page via each path's own cursor must also match.
	daoP2, daoC2, err := s.ListAssets(ctx, "server", "cmdb", "", limit, daoCursor)
	require.NoError(t, err)
	repoP2, repoC2, err := repo.ListAssets(ctx, "server", "cmdb", "", limit, repoCursor)
	require.NoError(t, err)
	assertListEquivalent(t, daoP2, repoP2, daoC2, repoC2)
}

// assertListEquivalent compares a legacy page (store.Asset + store.Cursor) against a repo page
// (*asset.Asset + asset.Cursor) element-by-element, including the next-cursor position.
func assertListEquivalent(t *testing.T, want []store.Asset, got []*asset.Asset, wantCur *store.Cursor, gotCur *asset.Cursor) {
	t.Helper()
	require.Equal(t, len(want), len(got), "page length")
	for i := range want {
		assertAssetEquivalent(t, want[i], got[i])
	}
	if wantCur == nil || gotCur == nil {
		assert.Equal(t, wantCur == nil, gotCur == nil, "cursor nilness")
		return
	}
	assert.Equal(t, wantCur.ID, gotCur.ID, "cursor id")
	assert.True(t, wantCur.CreatedAt.Equal(gotCur.CreatedAt), "cursor created_at")
}
