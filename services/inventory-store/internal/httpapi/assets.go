// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
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
	RouteUpsertAsset      = "upsert-asset"
	RouteUpsertAssetBatch = "upsert-asset-batch"
	RouteGetAsset         = "get-asset"
	RouteListAssets       = "list-assets"

	PathAssets      = "/v1/assets"
	PathAssetByID   = "/v1/assets/:id"
	PathAssetsBatch = "/v1/assets/batch"
)

func registerAssetRoutes(rg *gin.RouterGroup, d db) {
	rg.POST("/assets", setRoute(RouteUpsertAsset), handleUpsertAsset(d))
	rg.POST("/assets/batch", setRoute(RouteUpsertAssetBatch), handleUpsertAssetBatch(d))
	rg.GET("/assets/:id", setRoute(RouteGetAsset), handleGetAsset(d))
	rg.GET("/assets", setRoute(RouteListAssets), handleListAssets(d))
}

func handleUpsertAsset(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.UpsertAssetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		if err := validateRequired(
			requiredField{"iri", req.IRI},
			requiredField{"artifact_type", req.ArtifactType},
			requiredField{"source_system", req.SourceSystem},
			requiredField{"source_id", req.SourceID},
		); err != nil {
			renderError(c, err)
			return
		}
		in := store.AssetInput{
			IRI:          req.IRI,
			Name:         req.Name,
			ArtifactType: req.ArtifactType,
			SourceSystem: req.SourceSystem,
			SourceID:     req.SourceID,
			Extensions:   req.Extensions,
		}
		if req.IngestedAt != nil {
			t, err := time.Parse(time.RFC3339, *req.IngestedAt)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid ingested_at: %s", apperr.ErrInvalidInput, err.Error()))
				return
			}
			in.IngestedAt = &t
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
		source := c.Query("source_system")
		sourceID := c.Query("source_id")
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

		assets, nextCursor, err := d.ListAssets(c.Request.Context(), artifactType, source, sourceID, limit, cursor)
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
		if len(req.Assets) == 0 {
			renderError(c, fmt.Errorf("%w: assets must not be empty", apperr.ErrInvalidInput))
			return
		}
		inputs := make([]store.AssetInput, len(req.Assets))
		for i, a := range req.Assets {
			if err := validateRequired(
				requiredField{"iri", a.IRI},
				requiredField{"artifact_type", a.ArtifactType},
				requiredField{"source_system", a.SourceSystem},
				requiredField{"source_id", a.SourceID},
			); err != nil {
				renderError(c, fmt.Errorf("%w: item %d: %s", apperr.ErrInvalidInput, i, err.Error()))
				return
			}
			in := store.AssetInput{
				IRI:          a.IRI,
				Name:         a.Name,
				ArtifactType: a.ArtifactType,
				SourceSystem: a.SourceSystem,
				SourceID:     a.SourceID,
				Extensions:   a.Extensions,
			}
			if a.IngestedAt != nil {
				t, err := time.Parse(time.RFC3339, *a.IngestedAt)
				if err != nil {
					renderError(c, fmt.Errorf("%w: item %d invalid ingested_at: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				in.IngestedAt = &t
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
		IRI:          derefStr(a.IRI),
		Name:         a.Name,
		ArtifactType: a.ArtifactType,
		SourceSystem: a.SourceSystem,
		SourceID:     a.SourceID,
		Extensions:   a.Extensions,
		CreatedAt:    a.CreatedAt.UTC().Format(time.RFC3339),
		ValidFrom:    a.ValidFrom.UTC().Format(time.RFC3339),
	}
	if a.IngestedAt != nil {
		s := a.IngestedAt.UTC().Format(time.RFC3339)
		r.IngestedAt = &s
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

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
