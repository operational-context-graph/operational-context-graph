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
	routeUpsertRelationship      = "upsert-relationship"
	routeUpsertRelationshipBatch = "upsert-relationship-batch"
	routeGetRelationship         = "get-relationship"
	routeListRelationships       = "list-relationships"
	routeFilterRelationships     = "filter-relationships"
)

func registerRelationshipRoutes(rg *gin.RouterGroup, d db) {
	rg.POST("/relationships", setRoute(routeUpsertRelationship), handleUpsertRelationship(d))
	rg.POST("/relationships/batch", setRoute(routeUpsertRelationshipBatch), handleUpsertRelationshipBatch(d))
	rg.GET("/relationships/:id", setRoute(routeGetRelationship), handleGetRelationship(d))
	rg.GET("/relationships", setRoute(routeFilterRelationships), handleFilterRelationships(d))
	rg.GET("/assets/:id/relationships", setRoute(routeListRelationships), handleListRelationships(d))
}

func handleUpsertRelationship(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.UpsertRelationshipRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		in := store.RelationshipInput{
			SourceAssetID: req.SourceAssetID,
			TargetAssetID: req.TargetAssetID,
			ArtifactType:  req.ArtifactType,
			SourceSystem:  req.SourceSystem,
			Extensions:    json.RawMessage(req.Extensions),
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
		rel, created, err := d.UpsertRelationship(c.Request.Context(), in)
		if err != nil {
			renderError(c, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, v1.UpsertRelationshipResponse{Relationship: toRelationshipResponse(rel), Created: created})
	}
}

func handleGetRelationship(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			renderError(c, fmt.Errorf("%w: invalid uuid", apperr.ErrInvalidInput))
			return
		}
		rel, err := d.GetRelationshipByID(c.Request.Context(), id)
		if err != nil {
			renderError(c, err)
			return
		}
		c.JSON(http.StatusOK, toRelationshipResponse(rel))
	}
}

func handleListRelationships(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			renderError(c, fmt.Errorf("%w: invalid uuid", apperr.ErrInvalidInput))
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		rels, err := d.ListRelationships(c.Request.Context(), id, limit)
		if err != nil {
			renderError(c, err)
			return
		}
		items := make([]v1.RelationshipResponse, len(rels))
		for i, r := range rels {
			items[i] = toRelationshipResponse(r)
		}
		c.JSON(http.StatusOK, v1.RelationshipListResponse{Items: items})
	}
}

func handleFilterRelationships(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.Query("limit"))

		var assetID, sourceAssetID, targetAssetID *uuid.UUID
		if s := c.Query("asset_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid asset_id", apperr.ErrInvalidInput))
				return
			}
			assetID = &id
		}
		if s := c.Query("source_asset_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid source_asset_id", apperr.ErrInvalidInput))
				return
			}
			sourceAssetID = &id
		}
		if s := c.Query("target_asset_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid target_asset_id", apperr.ErrInvalidInput))
				return
			}
			targetAssetID = &id
		}

		rels, err := d.FilterRelationships(c.Request.Context(), assetID, sourceAssetID, targetAssetID, c.Query("type"), limit)
		if err != nil {
			renderError(c, err)
			return
		}
		items := make([]v1.RelationshipResponse, len(rels))
		for i, r := range rels {
			items[i] = toRelationshipResponse(r)
		}
		c.JSON(http.StatusOK, v1.RelationshipListResponse{Items: items})
	}
}

func handleUpsertRelationshipBatch(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.BatchUpsertRelationshipRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		inputs := make([]store.RelationshipInput, len(req.Relationships))
		for i, r := range req.Relationships {
			in := store.RelationshipInput{
				SourceAssetID: r.SourceAssetID,
				TargetAssetID: r.TargetAssetID,
				ArtifactType:  r.ArtifactType,
				SourceSystem:  r.SourceSystem,
				Extensions:    json.RawMessage(r.Extensions),
			}
			if r.CreatedAtSource != nil {
				t, err := time.Parse(time.RFC3339, *r.CreatedAtSource)
				if err != nil {
					renderError(c, fmt.Errorf("%w: item %d invalid created_at_source: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				in.CreatedAtSource = &t
			}
			if r.LastUpdatedAtSource != nil {
				t, err := time.Parse(time.RFC3339, *r.LastUpdatedAtSource)
				if err != nil {
					renderError(c, fmt.Errorf("%w: item %d invalid last_updated_at_source: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				in.LastUpdatedAtSource = &t
			}
			inputs[i] = in
		}
		rels, err := d.UpsertRelationshipBatch(c.Request.Context(), inputs)
		if err != nil {
			renderError(c, err)
			return
		}
		items := make([]v1.RelationshipResponse, len(rels))
		for i, r := range rels {
			items[i] = toRelationshipResponse(r)
		}
		c.JSON(http.StatusOK, v1.RelationshipListResponse{Items: items})
	}
}

func toRelationshipResponse(r store.Relationship) v1.RelationshipResponse {
	resp := v1.RelationshipResponse{
		ID:            r.ID,
		SourceAssetID: r.SourceAssetID,
		TargetAssetID: r.TargetAssetID,
		ArtifactType:  r.ArtifactType,
		SourceSystem:  r.SourceSystem,
		Extensions:    []byte(r.Extensions),
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.CreatedAtSource != nil {
		s := r.CreatedAtSource.UTC().Format(time.RFC3339)
		resp.CreatedAtSource = &s
	}
	if r.LastUpdatedAtSource != nil {
		s := r.LastUpdatedAtSource.UTC().Format(time.RFC3339)
		resp.LastUpdatedAtSource = &s
	}
	return resp
}
