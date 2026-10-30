// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	v1 "github.com/operational-context-graph/inventory-store/api/v1"
	"github.com/operational-context-graph/inventory-store/internal/apperr"
	"github.com/operational-context-graph/inventory-store/internal/inventory/asset"
)

// assetReader and assetWriter are the narrow ports the repo-backed asset handlers require. They are
// satisfied by the inventory domain repository. This stack speaks domain types only and never
// imports the legacy store package: it is a fully parallel implementation that maps the same v1
// wire contract to and from asset.Update / *asset.Asset.
type assetReader interface {
	GetAsset(ctx context.Context, id uuid.UUID) (*asset.Asset, error)
	ListAssets(ctx context.Context, artifactType, sourceSystem, sourceID string, limit int, after *asset.Cursor) ([]*asset.Asset, *asset.Cursor, error)
}

type assetWriter interface {
	Upsert(ctx context.Context, u asset.Update) (*asset.Asset, bool, error)
	UpsertBatch(ctx context.Context, updates []asset.Update) ([]asset.UpsertResult, error)
}

// registerAssetRoutesRepo mounts the repo-backed asset handlers on the same paths as the legacy
// stack. Exactly one of the two stacks is mounted per process, selected by the read-path flag.
func registerAssetRoutesRepo(rg *gin.RouterGroup, ar assetReader, aw assetWriter) {
	rg.POST("/assets", setRoute(RouteUpsertAsset), handleUpsertAssetRepo(aw))
	rg.POST("/assets/batch", setRoute(RouteUpsertAssetBatch), handleUpsertAssetBatchRepo(aw))
	rg.GET("/assets/:id", setRoute(RouteGetAsset), handleGetAssetRepo(ar))
	rg.GET("/assets", setRoute(RouteListAssets), handleListAssetsRepo(ar))
}

func handleUpsertAssetRepo(aw assetWriter) gin.HandlerFunc {
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
		u, err := assetUpdateFromRequest(req, "")
		if err != nil {
			renderError(c, err)
			return
		}
		asset, created, err := aw.Upsert(c.Request.Context(), u)
		if err != nil {
			renderError(c, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, v1.UpsertAssetResponse{Asset: toAssetResponseDomain(asset), Created: created})
	}
}

func handleGetAssetRepo(ar assetReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			renderError(c, fmt.Errorf("%w: invalid uuid", apperr.ErrInvalidInput))
			return
		}
		asset, err := ar.GetAsset(c.Request.Context(), id)
		if err != nil {
			renderError(c, err)
			return
		}
		c.JSON(http.StatusOK, toAssetResponseDomain(asset))
	}
}

func handleListAssetsRepo(ar assetReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		artifactType := c.Query("type")
		source := c.Query("source_system")
		sourceID := c.Query("source_id")
		limit, _ := strconv.Atoi(c.Query("limit"))
		if limit <= 0 {
			limit = 100
		}
		var cursor *asset.Cursor
		if after := c.Query("after"); after != "" {
			decoded, err := asset.DecodeCursor(after)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid cursor", apperr.ErrInvalidInput))
				return
			}
			cursor = &decoded
		}

		assets, nextCursor, err := ar.ListAssets(c.Request.Context(), artifactType, source, sourceID, limit, cursor)
		if err != nil {
			renderError(c, err)
			return
		}
		resp := v1.AssetListResponse{Items: make([]v1.AssetResponse, len(assets))}
		for i, a := range assets {
			resp.Items[i] = toAssetResponseDomain(a)
		}
		if nextCursor != nil {
			resp.NextCursor = nextCursor.Encode()
		}
		c.JSON(http.StatusOK, resp)
	}
}

func handleUpsertAssetBatchRepo(aw assetWriter) gin.HandlerFunc {
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
		updates := make([]asset.Update, len(req.Assets))
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
			u, err := assetUpdateFromRequest(a, fmt.Sprintf("item %d ", i))
			if err != nil {
				renderError(c, err)
				return
			}
			updates[i] = u
		}
		results, err := aw.UpsertBatch(c.Request.Context(), updates)
		if err != nil {
			renderError(c, err)
			return
		}
		resp := make([]v1.BatchResult, len(results))
		for i, r := range results {
			resp[i] = v1.BatchResult{ID: r.Asset.ID(), Outcome: batchOutcome(r)}
		}
		c.JSON(http.StatusOK, resp)
	}
}

// batchOutcome maps a domain UpsertResult to the same outcome strings the legacy batch path emits.
func batchOutcome(r asset.UpsertResult) string {
	switch {
	case r.Redelivery:
		return "unchanged"
	case r.Created:
		return "created"
	default:
		return "updated"
	}
}

// assetUpdateFromRequest maps a v1 request to a domain Update, parsing the RFC3339 timestamps. The
// prefix (e.g. "item 3 ") is woven into error messages so batch errors keep their index context.
func assetUpdateFromRequest(req v1.UpsertAssetRequest, prefix string) (asset.Update, error) {
	u := asset.Update{
		Key:          asset.NaturalKey{SourceSystem: req.SourceSystem, SourceID: req.SourceID},
		IRI:          optStr(req.IRI),
		Name:         optStr(req.Name),
		ArtifactType: req.ArtifactType,
		Extensions:   req.Extensions,
	}
	var err error
	if u.IngestedAt, err = parseOptTime(req.IngestedAt, prefix+"invalid ingested_at"); err != nil {
		return asset.Update{}, err
	}
	if u.CreatedAtSource, err = parseOptTime(req.CreatedAtSource, prefix+"invalid created_at_source"); err != nil {
		return asset.Update{}, err
	}
	if u.LastUpdatedAtSource, err = parseOptTime(req.LastUpdatedAtSource, prefix+"invalid last_updated_at_source"); err != nil {
		return asset.Update{}, err
	}
	return u, nil
}

// parseOptTime parses an optional RFC3339 pointer, returning nil for an absent value and an
// ErrInvalidInput-wrapped error (using msg) for a malformed one.
func parseOptTime(s *string, msg string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", apperr.ErrInvalidInput, msg, err.Error())
	}
	return &t, nil
}

// optStr returns nil for an empty string, else a pointer to it (the domain treats absent and empty
// the same, mirroring the store's null handling).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// toAssetResponseDomain maps a domain Asset to the v1 wire response. It produces the identical shape
// as the legacy toAssetResponse, reading from domain accessors instead of the store struct.
func toAssetResponseDomain(a *asset.Asset) v1.AssetResponse {
	v := a.Validity()
	r := v1.AssetResponse{
		ID:           a.ID(),
		IRI:          derefStr(a.IRI()),
		Name:         a.Name(),
		ArtifactType: a.ArtifactType(),
		SourceSystem: a.Key().SourceSystem,
		SourceID:     a.Key().SourceID,
		Extensions:   a.Extensions(),
		CreatedAt:    a.CreatedAt().UTC().Format(time.RFC3339),
		ValidFrom:    v.From.UTC().Format(time.RFC3339),
	}
	if t := a.IngestedAt(); t != nil {
		s := t.UTC().Format(time.RFC3339)
		r.IngestedAt = &s
	}
	if t := a.CreatedAtSource(); t != nil {
		s := t.UTC().Format(time.RFC3339)
		r.CreatedAtSource = &s
	}
	if t := a.LastUpdatedAtSource(); t != nil {
		s := t.UTC().Format(time.RFC3339)
		r.LastUpdatedAtSource = &s
	}
	if v.To != nil {
		s := v.To.UTC().Format(time.RFC3339)
		r.ValidTo = &s
	}
	return r
}
