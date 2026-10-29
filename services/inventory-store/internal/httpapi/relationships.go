package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

const (
	RouteUpsertRelationship      = "upsert-relationship"
	RouteUpsertRelationshipBatch = "upsert-relationship-batch"
	RouteGetRelationship         = "get-relationship"
	RouteListRelationships       = "list-relationships"
	RouteFilterRelationships     = "filter-relationships"
	RouteCloseRelationship       = "close-relationship"

	PathRelationships      = "/v1/relationships"
	PathRelationshipByID   = "/v1/relationships/:id"
	PathRelationshipsBatch = "/v1/relationships/batch"
	PathAssetRelationships = "/v1/assets/:id/relationships"
)

func registerRelationshipRoutes(rg *gin.RouterGroup, d db) {
	rg.POST("/relationships", setRoute(RouteUpsertRelationship), handleUpsertRelationship(d))
	rg.POST("/relationships/batch", setRoute(RouteUpsertRelationshipBatch), handleUpsertRelationshipBatch(d))
	rg.GET("/relationships/:id", setRoute(RouteGetRelationship), handleGetRelationship(d))
	rg.DELETE("/relationships/:id", setRoute(RouteCloseRelationship), handleCloseRelationship(d))
	rg.GET("/relationships", setRoute(RouteFilterRelationships), handleFilterRelationships(d))
	rg.GET("/assets/:id/relationships", setRoute(RouteListRelationships), handleListRelationships(d))
}

func handleUpsertRelationship(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req v1.UpsertRelationshipRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			renderError(c, bindErr(err))
			return
		}
		if err := validateRequired(
			requiredField{"artifact_type", req.ArtifactType},
			requiredField{"source_system", req.SourceSystem},
		); err != nil {
			renderError(c, err)
			return
		}

		// Key-based path: resolve asset IDs inside the store.
		if req.SourceAssetID == nil || req.TargetAssetID == nil {
			if len(req.SourceKey) != 2 || len(req.TargetKey) != 2 {
				renderError(c, fmt.Errorf("%w: provide source_asset_id/target_asset_id or source_key/target_key ([system, id])", apperr.ErrInvalidInput))
				return
			}
			rels, err := d.UpsertRelationshipBatchByKey(c.Request.Context(), []store.RelationshipKeyInput{toRelationshipKeyInput(req)})
			if err != nil {
				renderError(c, err)
				return
			}
			c.JSON(http.StatusOK, v1.UpsertRelationshipResponse{Relationship: toRelationshipResponse(rels[0]), Created: false})
			return
		}

		in, err := toRelationshipInput(req)
		if err != nil {
			renderError(c, err)
			return
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
		if len(req.Relationships) == 0 {
			renderError(c, fmt.Errorf("%w: relationships must not be empty", apperr.ErrInvalidInput))
			return
		}

		// Determine routing: key-based if any item omits source_asset_id/target_asset_id.
		// Validate all items use the same addressing mode.
		useKeys := req.Relationships[0].SourceAssetID == nil || req.Relationships[0].TargetAssetID == nil
		for i, r := range req.Relationships {
			itemUseKeys := r.SourceAssetID == nil || r.TargetAssetID == nil
			if itemUseKeys != useKeys {
				renderError(c, fmt.Errorf("%w: item %d: mixed UUID and key-based addressing in batch", apperr.ErrInvalidInput, i))
				return
			}
		}

		if useKeys {
			keyInputs := make([]store.RelationshipKeyInput, 0, len(req.Relationships))
			for i, r := range req.Relationships {
				if err := validateRequired(
					requiredField{"artifact_type", r.ArtifactType},
					requiredField{"source_system", r.SourceSystem},
				); err != nil {
					renderError(c, fmt.Errorf("%w: item %d: %s", apperr.ErrInvalidInput, i, err.Error()))
					return
				}
				if len(r.SourceKey) != 2 || len(r.TargetKey) != 2 {
					renderError(c, fmt.Errorf("%w: item %d: source_key and target_key must each be [source_system, source_id]", apperr.ErrInvalidInput, i))
					return
				}
				keyInputs = append(keyInputs, toRelationshipKeyInput(r))
			}
			rels, err := d.UpsertRelationshipBatchByKey(c.Request.Context(), keyInputs)
			if err != nil {
				renderError(c, err)
				return
			}
			items := make([]v1.RelationshipResponse, len(rels))
			for i, r := range rels {
				items[i] = toRelationshipResponse(r)
			}
			c.JSON(http.StatusOK, v1.RelationshipListResponse{Items: items})
			return
		}

		inputs := make([]store.RelationshipInput, 0, len(req.Relationships))
		for i, r := range req.Relationships {
			if err := validateRequired(
				requiredField{"artifact_type", r.ArtifactType},
				requiredField{"source_system", r.SourceSystem},
			); err != nil {
				renderError(c, fmt.Errorf("%w: item %d: %s", apperr.ErrInvalidInput, i, err.Error()))
				return
			}
			if r.SourceAssetID == nil || r.TargetAssetID == nil {
				renderError(c, fmt.Errorf("%w: item %d: source_asset_id and target_asset_id are required", apperr.ErrInvalidInput, i))
				return
			}
			in, err := toRelationshipInput(r)
			if err != nil {
				renderError(c, fmt.Errorf("%w: item %d: %s", apperr.ErrInvalidInput, i, err.Error()))
				return
			}
			inputs = append(inputs, in)
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

func handleCloseRelationship(d db) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			renderError(c, fmt.Errorf("%w: invalid uuid", apperr.ErrInvalidInput))
			return
		}
		if err := d.CloseRelationship(c.Request.Context(), id); err != nil {
			renderError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func toRelationshipResponse(r store.Relationship) v1.RelationshipResponse {
	resp := v1.RelationshipResponse{
		ID:            r.ID,
		SourceAssetID: r.SourceAssetID,
		TargetAssetID: r.TargetAssetID,
		ArtifactType:  r.ArtifactType,
		SourceSystem:  r.SourceSystem,
		SourceKey:     derefStr(r.SourceKey),
		TargetKey:     derefStr(r.TargetKey),
		SourceIRI:     derefStr(r.SourceIRI),
		TargetIRI:     derefStr(r.TargetIRI),
		Extensions:    r.Extensions,
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
		ValidFrom:     r.ValidFrom.UTC().Format(time.RFC3339),
	}
	if r.ValidTo != nil {
		s := r.ValidTo.UTC().Format(time.RFC3339)
		resp.ValidTo = &s
	}
	if r.IngestedAt != nil {
		s := r.IngestedAt.UTC().Format(time.RFC3339)
		resp.IngestedAt = &s
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

// toRelationshipInput converts a UUID-based request to a store.RelationshipInput.
// The caller must ensure SourceAssetID and TargetAssetID are non-nil.
func toRelationshipInput(r v1.UpsertRelationshipRequest) (store.RelationshipInput, error) {
	in := store.RelationshipInput{
		SourceAssetID: *r.SourceAssetID,
		TargetAssetID: *r.TargetAssetID,
		ArtifactType:  r.ArtifactType,
		SourceSystem:  r.SourceSystem,
		SourceKey:     strings.Join(r.SourceKey, ":"),
		TargetKey:     strings.Join(r.TargetKey, ":"),
		SourceIRI:     r.SourceIRI,
		TargetIRI:     r.TargetIRI,
		Extensions:    r.Extensions,
	}
	if r.IngestedAt != nil {
		t, err := time.Parse(time.RFC3339, *r.IngestedAt)
		if err != nil {
			return store.RelationshipInput{}, fmt.Errorf("%w: invalid ingested_at: %s", apperr.ErrInvalidInput, err.Error())
		}
		in.IngestedAt = &t
	}
	if r.CreatedAtSource != nil {
		t, err := time.Parse(time.RFC3339, *r.CreatedAtSource)
		if err != nil {
			return store.RelationshipInput{}, fmt.Errorf("%w: invalid created_at_source: %s", apperr.ErrInvalidInput, err.Error())
		}
		in.CreatedAtSource = &t
	}
	if r.LastUpdatedAtSource != nil {
		t, err := time.Parse(time.RFC3339, *r.LastUpdatedAtSource)
		if err != nil {
			return store.RelationshipInput{}, fmt.Errorf("%w: invalid last_updated_at_source: %s", apperr.ErrInvalidInput, err.Error())
		}
		in.LastUpdatedAtSource = &t
	} else if r.ValidTime != nil {
		t, err := time.Parse(time.RFC3339, *r.ValidTime)
		if err != nil {
			return store.RelationshipInput{}, fmt.Errorf("%w: invalid valid_time: %s", apperr.ErrInvalidInput, err.Error())
		}
		in.LastUpdatedAtSource = &t
	}
	return in, nil
}

// toRelationshipKeyInput converts a key-based request to a store.RelationshipKeyInput.
func toRelationshipKeyInput(r v1.UpsertRelationshipRequest) store.RelationshipKeyInput {
	in := store.RelationshipKeyInput{
		SourceKey:    r.SourceKey,
		TargetKey:    r.TargetKey,
		ArtifactType: r.ArtifactType,
		SourceSystem: r.SourceSystem,
		SourceIRI:    r.SourceIRI,
		TargetIRI:    r.TargetIRI,
		Extensions:   r.Extensions,
	}
	if r.IngestedAt != nil {
		if t, err := time.Parse(time.RFC3339, *r.IngestedAt); err == nil {
			in.IngestedAt = &t
		}
	}
	if r.CreatedAtSource != nil {
		if t, err := time.Parse(time.RFC3339, *r.CreatedAtSource); err == nil {
			in.CreatedAtSource = &t
		}
	}
	if r.LastUpdatedAtSource != nil {
		if t, err := time.Parse(time.RFC3339, *r.LastUpdatedAtSource); err == nil {
			in.LastUpdatedAtSource = &t
		}
	} else if r.ValidTime != nil {
		if t, err := time.Parse(time.RFC3339, *r.ValidTime); err == nil {
			in.LastUpdatedAtSource = &t
		}
	}
	return in
}
