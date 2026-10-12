// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/operational-context-graph/inventory-store/internal/testhelper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDB is a hand-written stub that satisfies the httpapi db interface.
type stubDB struct {
	upsertAssetFn func(ctx context.Context, in store.AssetInput) (store.Asset, bool, error)
	getAssetFn    func(ctx context.Context, id uuid.UUID) (store.Asset, error)
}

func (s *stubDB) UpsertAsset(ctx context.Context, in store.AssetInput) (store.Asset, bool, error) {
	if s.upsertAssetFn != nil {
		return s.upsertAssetFn(ctx, in)
	}
	return store.Asset{}, false, nil
}
func (s *stubDB) GetAsset(ctx context.Context, id uuid.UUID) (store.Asset, error) {
	if s.getAssetFn != nil {
		return s.getAssetFn(ctx, id)
	}
	return store.Asset{}, nil
}
func (s *stubDB) GetAssetByExternal(_ context.Context, _, _ string) (store.Asset, error) {
	return store.Asset{}, apperr.ErrNotFound
}
func (s *stubDB) ListAssets(_ context.Context, _, _ string, _ int, _ *store.Cursor) ([]store.Asset, *store.Cursor, error) {
	return nil, nil, nil
}
func (s *stubDB) UpsertAssetBatch(_ context.Context, _ []store.AssetInput) ([]store.BatchResult, error) {
	return nil, nil
}
func (s *stubDB) UpsertRelationship(_ context.Context, _ store.RelationshipInput) (store.Relationship, bool, error) {
	return store.Relationship{}, false, nil
}
func (s *stubDB) ListRelationships(_ context.Context, _ uuid.UUID, _ int) ([]store.Relationship, error) {
	return nil, nil
}
func (s *stubDB) FilterRelationships(_ context.Context, _, _, _ *uuid.UUID, _ string, _ int) ([]store.Relationship, error) {
	return nil, nil
}
func (s *stubDB) GetRelationshipByID(_ context.Context, _ uuid.UUID) (store.Relationship, error) {
	return store.Relationship{}, apperr.ErrNotFound
}
func (s *stubDB) UpsertRelationshipBatch(_ context.Context, _ []store.RelationshipInput) ([]store.Relationship, error) {
	return nil, nil
}
func (s *stubDB) Ping(_ context.Context) error { return nil }
func (s *stubDB) SchemaReady(_ context.Context) error { return nil }

func TestHandleUpsertAsset_Created(t *testing.T) {
	assetID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	stub := &stubDB{
		upsertAssetFn: func(_ context.Context, in store.AssetInput) (store.Asset, bool, error) {
			return store.Asset{
				ID:           assetID,
				ArtifactType: in.ArtifactType,
				SourceSystem: in.SourceSystem,
				SourceID:     in.SourceID,
				CreatedAt:    now,
				ValidFrom:    now,
			}, true, nil
		},
	}
	srv := httpapi.NewServer(stub, ":0")

	rr := testhelper.PostJSON(t, srv.Handler, "/v1/assets", map[string]any{
		"artifact_type": "server",
		"source_system": "cmdb",
		"source_id":     "srv-001",
	})

	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))

	var resp v1.UpsertAssetResponse
	testhelper.DecodeJSON(t, rr, &resp)
	assert.Equal(t, assetID, resp.Asset.ID)
	assert.True(t, resp.Created)
}

func TestHandleUpsertAsset_InvalidJSON(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")

	rr := testhelper.PostJSON(t, srv.Handler, "/v1/assets", "not-an-object")

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))

	var prob map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&prob))
	assert.Equal(t, float64(http.StatusBadRequest), prob["status"])
}

func TestHandleGetAsset_NotFound(t *testing.T) {
	stub := &stubDB{
		getAssetFn: func(_ context.Context, _ uuid.UUID) (store.Asset, error) {
			return store.Asset{}, apperr.ErrNotFound
		},
	}
	srv := httpapi.NewServer(stub, ":0")

	rr := testhelper.GetRequest(t, srv.Handler, "/v1/assets/"+uuid.New().String())

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))

	var prob map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&prob))
	assert.Equal(t, float64(http.StatusNotFound), prob["status"])
}

func TestHandleGetAsset_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/assets/not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleGetRelationship_NotFound(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/relationships/"+uuid.New().String())
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleGetRelationship_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/relationships/not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleFilterRelationships_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/relationships?asset_id=not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleFilterRelationships_EmptyResult(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/relationships")
	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.RelationshipListResponse
	testhelper.DecodeJSON(t, rr, &resp)
	assert.Empty(t, resp.Items)
}

func TestHandleGetAssetBySource_MissingParams(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/v1/assets/by-source?source=cmdb")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHealthEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/health")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequestIDMiddleware_SetsHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := testhelper.GetRequest(t, srv.Handler, "/health")
	assert.NotEmpty(t, rr.Header().Get("X-Request-Id"))
}

func TestRequestIDMiddleware_EchoesExistingHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "my-fixed-id")
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	assert.Equal(t, "my-fixed-id", rr.Header().Get("X-Request-Id"))
}
