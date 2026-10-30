// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/inventory-store/internal/inventory/asset"
	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/operational-context-graph/inventory-store/internal/test"
)

// These tests exercise the REPO-backed asset stack end to end through the HTTP layer with no
// database: a fake asset.AssetReader/Writer feeds the handlers. They prove the parallel stack
// maps the same v1 wire contract as the legacy stack, and that batch outcomes line up.

// fakeAssetBackend satisfies both asset ports the repo handlers require.
type fakeAssetBackend struct {
	upsert      func(ctx context.Context, u asset.Update) (*asset.Asset, bool, error)
	upsertBatch func(ctx context.Context, us []asset.Update) ([]asset.UpsertResult, error)
	get         func(ctx context.Context, id uuid.UUID) (*asset.Asset, error)
	list        func(ctx context.Context, at, ss, sid string, limit int, after *asset.Cursor) ([]*asset.Asset, *asset.Cursor, error)
}

func (f *fakeAssetBackend) Upsert(ctx context.Context, u asset.Update) (*asset.Asset, bool, error) {
	return f.upsert(ctx, u)
}

func (f *fakeAssetBackend) UpsertBatch(ctx context.Context, us []asset.Update) ([]asset.UpsertResult, error) {
	return f.upsertBatch(ctx, us)
}

func (f *fakeAssetBackend) GetAsset(ctx context.Context, id uuid.UUID) (*asset.Asset, error) {
	return f.get(ctx, id)
}

func (f *fakeAssetBackend) ListAssets(ctx context.Context, at, ss, sid string, limit int, after *asset.Cursor) ([]*asset.Asset, *asset.Cursor, error) {
	return f.list(ctx, at, ss, sid, limit, after)
}

// mkDomainAsset builds a well-formed domain Asset via the from-storage factory.
func mkDomainAsset(t *testing.T, id uuid.UUID, system, sid string, from time.Time) *asset.Asset {
	t.Helper()
	name := "srv"
	a, err := asset.NewAssetFromStorage(asset.AssetFromStorageParams{
		ID:           id,
		SourceSystem: system,
		SourceID:     sid,
		Name:         &name,
		ArtifactType: "server",
		Extensions:   json.RawMessage(`{"k":"v"}`),
		CreatedAt:    from,
		ValidFrom:    from,
	})
	require.NoError(t, err)
	return a
}

// repoServer mounts only the repo-backed asset stack. The legacy db is a stub; relationships and
// health are not exercised here.
func repoServer(b *fakeAssetBackend) *http.Server {
	return httpapi.NewServer(&stubDB{}, b, httpapi.AssetPathRepo, ":0")
}

func TestRepoHandleUpsertAsset_Created(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	var gotKey asset.NaturalKey
	b := &fakeAssetBackend{
		upsert: func(_ context.Context, u asset.Update) (*asset.Asset, bool, error) {
			gotKey = u.Key
			return mkDomainAsset(t, id, u.Key.SourceSystem, u.Key.SourceID, now), true, nil
		},
	}

	rr := test.PostJSON(t, repoServer(b).Handler, httpapi.PathAssets, map[string]any{
		"iri":           "urn:ocg:asset/cmdb/srv-001",
		"artifact_type": "server",
		"source_system": "cmdb",
		"source_id":     "srv-001",
	})

	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))
	assert.Equal(t, asset.NaturalKey{SourceSystem: "cmdb", SourceID: "srv-001"}, gotKey)

	var resp v1.UpsertAssetResponse
	test.DecodeJSON(t, rr, &resp)
	assert.True(t, resp.Created)
	assert.Equal(t, id, resp.Asset.ID)
	assert.Equal(t, "server", resp.Asset.ArtifactType)
	assert.Equal(t, "cmdb", resp.Asset.SourceSystem)
	assert.Equal(t, "srv-001", resp.Asset.SourceID)
	assert.Equal(t, now.Format(time.RFC3339), resp.Asset.ValidFrom)
}

// TestRepoHandleUpsertAsset_WireEquivalentToLegacy proves the repo response body is byte-identical
// to what the legacy toAssetResponse mapping would produce for the same underlying asset. It mounts
// the repo stack, upserts, then marshals the expected v1.AssetResponse by hand from the same values.
func TestRepoHandleUpsertAsset_WireEquivalentToLegacy(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	b := &fakeAssetBackend{
		upsert: func(_ context.Context, u asset.Update) (*asset.Asset, bool, error) {
			return mkDomainAsset(t, id, u.Key.SourceSystem, u.Key.SourceID, now), false, nil
		},
	}

	rr := test.PostJSON(t, repoServer(b).Handler, httpapi.PathAssets, map[string]any{
		"iri":           "urn:ocg:asset/cmdb/srv-001",
		"artifact_type": "server",
		"source_system": "cmdb",
		"source_id":     "srv-001",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	name := "srv"
	want := v1.UpsertAssetResponse{
		Asset: v1.AssetResponse{
			ID:           id,
			IRI:          "",
			Name:         &name,
			ArtifactType: "server",
			SourceSystem: "cmdb",
			SourceID:     "srv-001",
			Extensions:   json.RawMessage(`{"k":"v"}`),
			CreatedAt:    now.Format(time.RFC3339),
			ValidFrom:    now.Format(time.RFC3339),
		},
		Created: false,
	}
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), rr.Body.String())
}

func TestRepoHandleGetAsset_NotFound(t *testing.T) {
	b := &fakeAssetBackend{
		get: func(_ context.Context, _ uuid.UUID) (*asset.Asset, error) {
			return nil, store.ErrAssetNotFound
		},
	}
	rr := test.GetRequest(t, repoServer(b).Handler, httpapi.PathAssets+"/"+uuid.New().String())
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRepoHandleGetAsset_InvalidUUID(t *testing.T) {
	rr := test.GetRequest(t, repoServer(&fakeAssetBackend{}).Handler, httpapi.PathAssets+"/not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRepoHandleListAssets_CursorRoundTrips(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a1 := mkDomainAsset(t, uuid.New(), "cmdb", "a", now)
	next := &asset.Cursor{CreatedAt: now, ID: a1.ID()}
	var gotAfter *asset.Cursor
	b := &fakeAssetBackend{
		list: func(_ context.Context, _, _, _ string, _ int, after *asset.Cursor) ([]*asset.Asset, *asset.Cursor, error) {
			gotAfter = after
			return []*asset.Asset{a1}, next, nil
		},
	}
	srv := repoServer(b)

	// First page: no cursor in, next cursor out.
	rr := test.GetRequest(t, srv.Handler, httpapi.PathAssets)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.AssetListResponse
	test.DecodeJSON(t, rr, &resp)
	require.Len(t, resp.Items, 1)
	require.NotEmpty(t, resp.NextCursor)
	assert.Nil(t, gotAfter)

	// Second page: the emitted cursor decodes back to the same position (wire-compatible).
	rr2 := test.GetRequest(t, srv.Handler, httpapi.PathAssets+"?after="+resp.NextCursor)
	require.Equal(t, http.StatusOK, rr2.Code)
	require.NotNil(t, gotAfter)
	assert.Equal(t, next.ID, gotAfter.ID)
	assert.True(t, next.CreatedAt.Equal(gotAfter.CreatedAt))
}

func TestRepoHandleUpsertAssetBatch_Outcomes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	created := mkDomainAsset(t, uuid.New(), "cmdb", "new", now)
	updated := mkDomainAsset(t, uuid.New(), "cmdb", "upd", now)
	unchanged := mkDomainAsset(t, uuid.New(), "cmdb", "same", now)
	b := &fakeAssetBackend{
		upsertBatch: func(_ context.Context, us []asset.Update) ([]asset.UpsertResult, error) {
			require.Len(t, us, 3)
			return []asset.UpsertResult{
				{Asset: created, Created: true},
				{Asset: updated},
				{Asset: unchanged, Redelivery: true},
			}, nil
		},
	}

	rr := test.PostJSON(t, repoServer(b).Handler, httpapi.PathAssetsBatch, map[string]any{
		"assets": []map[string]any{
			{"iri": "a", "artifact_type": "server", "source_system": "cmdb", "source_id": "new"},
			{"iri": "b", "artifact_type": "server", "source_system": "cmdb", "source_id": "upd"},
			{"iri": "c", "artifact_type": "server", "source_system": "cmdb", "source_id": "same"},
		},
	})
	require.Equal(t, http.StatusOK, rr.Code)

	var results []v1.BatchResult
	test.DecodeJSON(t, rr, &results)
	require.Len(t, results, 3)
	assert.Equal(t, "created", results[0].Outcome)
	assert.Equal(t, created.ID(), results[0].ID)
	assert.Equal(t, "updated", results[1].Outcome)
	assert.Equal(t, "unchanged", results[2].Outcome)
}
