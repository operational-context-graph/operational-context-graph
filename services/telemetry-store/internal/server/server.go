// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/operational-context-graph/telemetry-store/internal/store"
)

// New wires up the HTTP mux.
func New(db *store.DB) http.Handler {
	mux := http.NewServeMux()
	h := &handler{db: db}

	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /v1/telemetry/logs", h.getLogs)
	mux.HandleFunc("GET /v1/telemetry/traces/{trace_id}", h.getTrace)
	mux.HandleFunc("GET /v1/telemetry/metrics", h.getMetrics)

	return mux
}

type handler struct {
	db *store.DB
}

func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(); err != nil {
		slog.Error("healthz: doris unreachable", "err", err)
		jsonError(w, http.StatusServiceUnavailable, "doris unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *handler) getLogs(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseTimeRange(w, r)
	if !ok {
		return
	}

	q := store.LogQuery{
		From:     from,
		To:       to,
		Host:     r.URL.Query().Get("host"),
		Severity: r.URL.Query().Get("severity"),
		Limit:    parseInt(r.URL.Query().Get("limit"), 1000),
	}

	if raw := r.URL.Query().Get("cursor"); raw != "" {
		ts, id, err := decodeCursor(raw)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		q.AfterTS = &ts
		q.AfterRecordID = &id
	}

	logs, err := h.db.QueryLogs(q)
	if err != nil {
		slog.Error("query logs", "err", err)
		jsonError(w, http.StatusInternalServerError, "query error")
		return
	}

	resp := map[string]any{"items": logs}
	if len(logs) == q.Limit && len(logs) > 0 {
		last := logs[len(logs)-1]
		resp["next_cursor"] = encodeCursor(last.Timestamp, last.RecordID)
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *handler) getTrace(w http.ResponseWriter, r *http.Request) {
	traceID := r.PathValue("trace_id")
	if traceID == "" {
		jsonError(w, http.StatusBadRequest, "trace_id required")
		return
	}

	from, to, ok := parseTimeRange(w, r)
	if !ok {
		return
	}

	spans, err := h.db.QueryTraceByID(traceID, from, to)
	if err != nil {
		slog.Error("query trace", "err", err)
		jsonError(w, http.StatusInternalServerError, "query error")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"items": spans})
}

func (h *handler) getMetrics(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseTimeRange(w, r)
	if !ok {
		return
	}

	q := store.MetricQuery{
		From:       from,
		To:         to,
		Host:       r.URL.Query().Get("host"),
		MetricName: r.URL.Query().Get("metric_name"),
		Limit:      parseInt(r.URL.Query().Get("limit"), 1000),
	}

	if raw := r.URL.Query().Get("cursor"); raw != "" {
		ts, id, err := decodeCursor(raw)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		q.AfterTS = &ts
		q.AfterID = &id
	}

	metrics, err := h.db.QueryMetrics(q)
	if err != nil {
		slog.Error("query metrics", "err", err)
		jsonError(w, http.StatusInternalServerError, "query error")
		return
	}

	resp := map[string]any{"items": metrics}
	if len(metrics) == q.Limit && len(metrics) > 0 {
		last := metrics[len(metrics)-1]
		resp["next_cursor"] = encodeCursor(last.Timestamp, last.RecordID)
	}
	jsonResponse(w, http.StatusOK, resp)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseTimeRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		jsonError(w, http.StatusBadRequest, "from and to are required")
		return time.Time{}, time.Time{}, false
	}
	var err error
	from, err = time.Parse(time.RFC3339, fromStr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "from must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	to, err = time.Parse(time.RFC3339, toStr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "to must be RFC3339")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func encodeCursor(ts time.Time, recordID string) string {
	b, _ := json.Marshal(map[string]string{
		"t": ts.UTC().Format(time.RFC3339Nano),
		"i": recordID,
	})
	return fmt.Sprintf("%x", b)
}

func decodeCursor(s string) (time.Time, string, error) {
	var b []byte
	if _, err := fmt.Sscanf(s, "%x", &b); err != nil {
		return time.Time{}, "", err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return time.Time{}, "", err
	}
	ts, err := time.Parse(time.RFC3339Nano, m["t"])
	if err != nil {
		return time.Time{}, "", err
	}
	return ts, m["i"], nil
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
