// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

const (
	routeUpsertAsset      = "upsert-asset"
	routeUpsertAssetBatch = "upsert-asset-batch"
	routeGetAsset         = "get-asset"
	routeListAssets       = "list-assets"
)

func registerAssetRoutes(rg *gin.RouterGroup, d db) {
	rg.POST("/assets", setRoute(routeUpsertAsset), handleUpsertAsset(d))
	rg.POST("/assets/batch", setRoute(routeUpsertAssetBatch), handleUpsertAssetBatch(d))
	rg.GET("/assets/:id", setRoute(routeGetAsset), handleGetAsset(d))
	rg.GET("/assets", setRoute(routeListAssets), handleListAssets(d))
}

func handleUpsertAsset(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.UpsertAssetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		in := store.AssetInput{
			ArtifactType: req.ArtifactType,
			SourceSystem: req.SourceSystem,
			SourceID:     req.SourceID,
			Extensions:   json.RawMessage(req.Extensions),
		}
		if req.CreatedAtSource != nil {
			t, err := time.Parse(time.RFC3339, *req.CreatedAtSource)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid created_at_source: %s", apperr.ErrInvalidInput, err.Error()))
				return
			}
			in.CreatedAtSource = &t
		}
		if req.LastUpdatedAtSource != nil {
			t, err := time.Parse(time.RFC3339, *req.LastUpdatedAtSource)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid last_updated_at_source: %s", apperr.ErrInvalidInput, err.Error()))
				return
			}
			in.LastUpdatedAtSource = &t
		}
		asset, created, err := d.UpsertAsset(c.Request.Context(), in)
		if err != nil {
			renderError(c, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, v1.UpsertAssetResponse{Asset: toAssetResponse(asset), Created: created})
	}
}

func handleGetAsset(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			renderError(c, fmt.Errorf("%w: invalid uuid", apperr.ErrInvalidInput))
			return
		}
		asset, err := d.GetAsset(c.Request.Context(), id)
		if err != nil {
			renderError(c, err)
			return
		}
		c.JSON(http.StatusOK, toAssetResponse(asset))
	}
}


func handleListAssets(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		artifactType := c.Query("type")
		source := c.Query("source")
		limit, _ := strconv.Atoi(c.Query("limit"))
		if limit <= 0 {
			limit = 100
		}
		var cursor *store.Cursor
		if after := c.Query("after"); after != "" {
			decoded, err := store.DecodeCursor(after)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid cursor", apperr.ErrInvalidInput))
				return
			}
			cursor = &decoded
		}

		assets, nextCursor, err := d.ListAssets(c.Request.Context(), artifactType, source, limit, cursor)
		if err != nil {
			renderError(c, err)
			return
		}
		resp := v1.AssetListResponse{Items: make([]v1.AssetResponse, len(assets))}
		for i, a := range assets {
			resp.Items[i] = toAssetResponse(a)
		}
		if nextCursor != nil {
			resp.NextCursor = nextCursor.Encode()
		}
		c.JSON(http.StatusOK, resp)
	}
}

func handleUpsertAssetBatch(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.BatchUpsertAssetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		inputs := make([]store.AssetInput, len(req.Assets))
		for i, a := range req.Assets {
			in := store.AssetInput{
				ArtifactType: a.ArtifactType,
				SourceSystem: a.SourceSystem,
				SourceID:     a.SourceID,
				Extensions:   json.RawMessage(a.Extensions),
			}
			if a.CreatedAtSource != nil {
				t, err := time.Parse(time.RFC3339, *a.CreatedAtSource)
				if err != nil {
					renderError(c, fmt.Errorf("%w: item %d invalid created_at_source: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				in.CreatedAtSource = &t
			}
			if a.LastUpdatedAtSource != nil {
				t, err := time.Parse(time.RFC3339, *a.LastUpdatedAtSource)
				if err != nil {
					renderError(c, fmt.Errorf("%w: item %d invalid last_updated_at_source: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				in.LastUpdatedAtSource = &t
			}
			inputs[i] = in
		}
		results, err := d.UpsertAssetBatch(c.Request.Context(), inputs)
		if err != nil {
			renderError(c, err)
			return
		}
		resp := make([]v1.BatchResult, len(results))
		for i, r := range results {
			resp[i] = v1.BatchResult{ID: r.ID, Outcome: r.Outcome}
		}
		c.JSON(http.StatusOK, resp)
	}
}

func toAssetResponse(a store.Asset) v1.AssetResponse {
	r := v1.AssetResponse{
		ID:           a.ID,
		ArtifactType: a.ArtifactType,
		SourceSystem: a.SourceSystem,
		SourceID:     a.SourceID,
		Extensions:   []byte(a.Extensions),
		CreatedAt:    a.CreatedAt.UTC().Format(time.RFC3339),
		ValidFrom:    a.ValidFrom.UTC().Format(time.RFC3339),
	}
	if a.CreatedAtSource != nil {
		s := a.CreatedAtSource.UTC().Format(time.RFC3339)
		r.CreatedAtSource = &s
	}
	if a.LastUpdatedAtSource != nil {
		s := a.LastUpdatedAtSource.UTC().Format(time.RFC3339)
		r.LastUpdatedAtSource = &s
	}
	if a.ValidTo != nil {
		s := a.ValidTo.UTC().Format(time.RFC3339)
		r.ValidTo = &s
	}
	return r
}
