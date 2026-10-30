package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/httpapi"
	"github.com/operational-context-graph/inventory-store/internal/store"
	"github.com/operational-context-graph/inventory-store/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDB is a hand-written stub that satisfies the httpapi db interface.
type stubDB struct {
	upsertAssetFn        func(ctx context.Context, in store.AssetInput) (store.Asset, bool, error)
	getAssetFn           func(ctx context.Context, id uuid.UUID) (store.Asset, error)
	closeRelationshipFn  func(ctx context.Context, id uuid.UUID) error
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
func (s *stubDB) ListAssets(_ context.Context, _, _, _ string, _ int, _ *store.Cursor) ([]store.Asset, *store.Cursor, error) {
	return nil, nil, nil
}
func (s *stubDB) UpsertAssetBatch(_ context.Context, _ []store.AssetInput) ([]store.BatchResult, error) {
	return nil, nil
}
func (s *stubDB) UpsertRelationship(_ context.Context, _ store.RelationshipInput) (store.Relationship, bool, error) {
	return store.Relationship{}, false, nil
}
func (s *stubDB) CloseRelationship(ctx context.Context, id uuid.UUID) error {
	if s.closeRelationshipFn != nil {
		return s.closeRelationshipFn(ctx, id)
	}
	return nil
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
func (s *stubDB) UpsertRelationshipBatchByKey(_ context.Context, _ []store.RelationshipKeyInput) ([]store.Relationship, error) {
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

	rr := test.PostJSON(t, srv.Handler, httpapi.PathAssets, map[string]any{
		"iri":           "urn:ocg:asset/cmdb/srv-001",
		"artifact_type": "server",
		"source_system": "cmdb",
		"source_id":     "srv-001",
	})

	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "application/json; charset=utf-8", rr.Header().Get("Content-Type"))

	var resp v1.UpsertAssetResponse
	test.DecodeJSON(t, rr, &resp)
	assert.Equal(t, assetID, resp.Asset.ID)
	assert.True(t, resp.Created)
}

func TestHandleUpsertAsset_InvalidJSON(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")

	rr := test.PostJSON(t, srv.Handler, httpapi.PathAssets, "not-an-object")

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

	rr := test.GetRequest(t, srv.Handler, strings.Replace(httpapi.PathAssetByID, ":id", uuid.New().String(), 1))

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))

	var prob map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&prob))
	assert.Equal(t, float64(http.StatusNotFound), prob["status"])
}

func TestHandleGetAsset_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, strings.Replace(httpapi.PathAssetByID, ":id", "not-a-uuid", 1))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleGetRelationship_NotFound(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, strings.Replace(httpapi.PathRelationshipByID, ":id", uuid.New().String(), 1))
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleGetRelationship_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, strings.Replace(httpapi.PathRelationshipByID, ":id", "not-a-uuid", 1))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleFilterRelationships_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, httpapi.PathRelationships+"?asset_id=not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleFilterRelationships_EmptyResult(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, httpapi.PathRelationships)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp v1.RelationshipListResponse
	test.DecodeJSON(t, rr, &resp)
	assert.Empty(t, resp.Items)
}



func TestHealthEndpoint_OK(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, "/health")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequestIDMiddleware_SetsHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, "/health")
	assert.NotEmpty(t, rr.Header().Get("X-Request-Id"))
}

func TestHandleUpsertAsset_WrongContentType_Returns415(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.PostRaw(t, srv.Handler, httpapi.PathAssets, "text/plain", `{"artifact_type":"server","source_system":"cmdb","source_id":"x"}`)
	assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleUpsertAsset_OversizedBody_Returns413(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	// Build a valid JSON object whose string value exceeds the 4 MiB cap.
	// The JSON parser reads past the opening quote before MaxBytesReader cuts it.
	big := `{"artifact_type":"` + strings.Repeat("a", 5*1024*1024) + `"}`
	rr := test.PostRaw(t, srv.Handler, httpapi.PathAssets, "application/json", big)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestUnmatchedRoute_Returns404WithProblemJSON(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.GetRequest(t, srv.Handler, "/v1/does-not-exist")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestRequestIDMiddleware_EchoesExistingHeader(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "my-fixed-id")
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	assert.Equal(t, "my-fixed-id", rr.Header().Get("X-Request-Id"))
}

func TestHandleUpsertAsset_MissingRequiredFields_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")

	tests := []struct {
		name    string
		body    map[string]any
		wantErr []string
	}{
		{
			name:    "all empty",
			body:    map[string]any{},
			wantErr: []string{"iri", "artifact_type", "source_system", "source_id"},
		},
		{
			name:    "missing source_id",
			body:    map[string]any{"iri": "urn:ocg:asset/cmdb/x", "artifact_type": "server", "source_system": "cmdb"},
			wantErr: []string{"source_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := test.PostJSON(t, srv.Handler, httpapi.PathAssets, tc.body)
			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))

			var prob struct {
				Status int `json:"status"`
				Errors []struct {
					Field string `json:"field"`
				} `json:"errors"`
			}
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&prob))
			assert.Equal(t, http.StatusBadRequest, prob.Status)

			got := make([]string, len(prob.Errors))
			for i, e := range prob.Errors {
				got[i] = e.Field
			}
			for _, want := range tc.wantErr {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestHandleUpsertRelationship_MissingRequiredFields_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")

	rr := test.PostJSON(t, srv.Handler, httpapi.PathRelationships, map[string]any{
		"source_asset_id": uuid.New().String(),
		"target_asset_id": uuid.New().String(),
	})
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))

	var prob struct {
		Status int `json:"status"`
		Errors []struct {
			Field string `json:"field"`
		} `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&prob))
	assert.Equal(t, http.StatusBadRequest, prob.Status)

	got := make([]string, len(prob.Errors))
	for i, e := range prob.Errors {
		got[i] = e.Field
	}
	assert.Contains(t, got, "artifact_type")
	assert.Contains(t, got, "source_system")
}

func TestHandleUpsertRelationship_NoEndpoints_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")

	// Neither UUIDs nor key arrays provided — must return 400.
	rr := test.PostJSON(t, srv.Handler, httpapi.PathRelationships, map[string]any{
		"artifact_type": "depends-on",
		"source_system": "cmdb",
	})
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleUpsertAssetBatch_EmptySlice_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.PostJSON(t, srv.Handler, httpapi.PathAssetsBatch, map[string]any{"assets": []any{}})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleUpsertRelationshipBatch_EmptySlice_Returns400(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.PostJSON(t, srv.Handler, httpapi.PathRelationshipsBatch, map[string]any{"relationships": []any{}})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleCloseRelationship_NoContent(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.DeleteRequest(t, srv.Handler, strings.Replace(httpapi.PathRelationshipByID, ":id", uuid.New().String(), 1))
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandleCloseRelationship_NotFound(t *testing.T) {
	stub := &stubDB{
		closeRelationshipFn: func(_ context.Context, _ uuid.UUID) error {
			return apperr.ErrNotFound
		},
	}
	srv := httpapi.NewServer(stub, ":0")
	rr := test.DeleteRequest(t, srv.Handler, strings.Replace(httpapi.PathRelationshipByID, ":id", uuid.New().String(), 1))
	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}

func TestHandleCloseRelationship_InvalidUUID(t *testing.T) {
	srv := httpapi.NewServer(&stubDB{}, ":0")
	rr := test.DeleteRequest(t, srv.Handler, strings.Replace(httpapi.PathRelationshipByID, ":id", "not-a-uuid", 1))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}
