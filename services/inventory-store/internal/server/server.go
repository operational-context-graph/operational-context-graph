// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// New wires up the HTTP mux.
func New(s *store.Store) http.Handler {
	mux := http.NewServeMux()
	h := &handler{store: s}

	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("POST /assets", h.postAsset)
	mux.HandleFunc("GET /assets/by-external", h.getAssetByExternal)
	mux.HandleFunc("GET /assets/{id}", h.getAsset)
	mux.HandleFunc("GET /assets", h.listAssets)
	mux.HandleFunc("POST /relationships", h.postRelationship)
	mux.HandleFunc("GET /relationships", h.listRelationships)

	return mux
}

type handler struct {
	store *store.Store
}

func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		slog.Error("healthz: postgres unreachable", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "postgres unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *handler) postAsset(w http.ResponseWriter, r *http.Request) {
	var in store.AssetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.AssetType == "" || in.Source == "" || in.ExternalID == "" {
		jsonError(w, http.StatusBadRequest, "asset_type, source, and external_id are required")
		return
	}

	asset, created, err := h.store.UpsertAsset(r.Context(), in)
	if err != nil {
		slog.Error("upsert asset", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "database error")
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	jsonResponse(w, status, asset)
}

func (h *handler) getAsset(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid id")
		return
	}
	asset, err := h.store.GetAsset(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		slog.Error("get asset", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "database error")
		return
	}
	jsonResponse(w, http.StatusOK, asset)
}

func (h *handler) getAssetByExternal(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("source")
	ext := r.URL.Query().Get("external_id")
	if src == "" || ext == "" {
		jsonError(w, http.StatusBadRequest, "source and external_id are required")
		return
	}
	asset, err := h.store.GetAssetByExternal(r.Context(), src, ext)
	if errors.Is(err, store.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		slog.Error("get asset by external", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "database error")
		return
	}
	jsonResponse(w, http.StatusOK, asset)
}

func (h *handler) listAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	assetType := q.Get("type")
	source := q.Get("source")
	limit := parseInt(q.Get("limit"), 100)

	var after *store.Cursor
	if raw := q.Get("after"); raw != "" {
		c, err := store.DecodeCursor(raw)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		after = &c
	}

	assets, nextCursor, err := h.store.ListAssets(r.Context(), assetType, source, limit, after)
	if err != nil {
		slog.Error("list assets", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "database error")
		return
	}

	resp := map[string]any{"items": assets}
	if nextCursor != nil {
		resp["next_cursor"] = nextCursor.Encode()
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *handler) postRelationship(w http.ResponseWriter, r *http.Request) {
	var in store.RelationshipInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.RelationshipType == "" || in.Source == "" {
		jsonError(w, http.StatusBadRequest, "relationship_type and source are required")
		return
	}

	rel, created, err := h.store.UpsertRelationship(r.Context(), in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "referenced asset not found: "+err.Error())
			return
		}
		slog.Error("upsert relationship", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "database error")
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	jsonResponse(w, status, rel)
}

func (h *handler) listRelationships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// GET /relationships?asset_id=<id>
	if rawID := q.Get("asset_id"); rawID != "" {
		id, err := uuid.Parse(rawID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid asset_id")
			return
		}
		rels, err := h.store.ListRelationships(r.Context(), id)
		if err != nil {
			slog.Error("list relationships", "err", err)
			jsonError(w, http.StatusServiceUnavailable, "database error")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": rels})
		return
	}

	// GET /relationships?source_asset_id=<id>&target_asset_id=<id>&type=<type>
	srcRaw := q.Get("source_asset_id")
	tgtRaw := q.Get("target_asset_id")
	relType := q.Get("type")
	if srcRaw != "" && tgtRaw != "" && relType != "" {
		srcID, err1 := uuid.Parse(srcRaw)
		tgtID, err2 := uuid.Parse(tgtRaw)
		if err1 != nil || err2 != nil {
			jsonError(w, http.StatusBadRequest, "invalid uuid")
			return
		}
		rel, err := h.store.GetRelationship(r.Context(), srcID, tgtID, relType)
		if errors.Is(err, store.ErrNotFound) {
			jsonResponse(w, http.StatusOK, map[string]any{"items": []any{}})
			return
		}
		if err != nil {
			slog.Error("get relationship", "err", err)
			jsonError(w, http.StatusServiceUnavailable, "database error")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": []store.Relationship{rel}})
		return
	}

	jsonError(w, http.StatusBadRequest, "provide asset_id or (source_asset_id, target_asset_id, type)")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
	}
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}
