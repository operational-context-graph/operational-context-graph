// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package inventory implements the Inventory Store writer. It maps batches of
// canonical records to the Inventory Store REST API, enforcing write ordering
// (assets before relationships).
package inventory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// Config holds the connection settings for the Inventory Store REST API.
type Config struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRecords int
	MaxBytes   int64
	MaxLinger  time.Duration
}

// Writer delivers canonical inventory records to the Inventory Store REST API.
// It implements store.StoreWriter.
type Writer struct {
	cfg    Config
	client *http.Client
}

var _ store.StoreWriter = (*Writer)(nil)

// New constructs a Writer from cfg.
func New(cfg Config) *Writer {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if cfg.MaxRecords <= 0 {
		cfg.MaxRecords = 1000
	}
	if cfg.MaxLinger <= 0 {
		cfg.MaxLinger = 5 * time.Minute
	}
	return &Writer{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// StoreID returns the store identity this writer serves.
func (w *Writer) StoreID() store.StoreID { return "inventory" }

// Limits reports the Inventory Store's batching thresholds to the drain.
func (w *Writer) Limits() store.BatchLimits {
	return store.BatchLimits{
		MaxRecords: w.cfg.MaxRecords,
		MaxBytes:   w.cfg.MaxBytes,
		MaxLinger:  w.cfg.MaxLinger,
	}
}

// Write delivers batch to the Inventory Store. Assets are written before
// relationships to satisfy the FK constraint enforced by the store.
func (w *Writer) Write(ctx context.Context, batch store.Batch) error {
	if len(batch.Records) == 0 {
		return nil
	}

	var assets, relationships []store.Record
	for _, rec := range batch.Records {
		if isRelationship(rec) {
			relationships = append(relationships, rec)
		} else {
			assets = append(assets, rec)
		}
	}

	if len(assets) > 0 {
		if err := w.postBatch(ctx, "/v1/assets/batch", "assets", assets); err != nil {
			return err
		}
	}

	if len(relationships) > 0 {
		if err := w.postBatch(ctx, "/v1/relationships/batch", "relationships", relationships); err != nil {
			return err
		}
	}

	return nil
}

func (w *Writer) postBatch(ctx context.Context, path, key string, recs []store.Record) error {
	body := buildWrappedJSONArray(key, recs)

	url := strings.TrimRight(w.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("inventory writer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: inventory writer: %w", store.ErrOverloaded, err)
	}
	defer resp.Body.Close()

	return w.handleResponse(resp, path)
}

func (w *Writer) handleResponse(resp *http.Response, path string) error {
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return &store.PermanentError{Reason: fmt.Sprintf("referenced asset not found (%s)", path)}
	case resp.StatusCode == http.StatusBadRequest:
		raw, _ := io.ReadAll(resp.Body)
		return &store.PermanentError{Reason: fmt.Sprintf("validation error (%s): %s", path, string(raw))}
	case resp.StatusCode == http.StatusTooManyRequests:
		return store.ErrOverloaded
	case resp.StatusCode == http.StatusServiceUnavailable:
		return store.ErrOverloaded
	case resp.StatusCode >= 500:
		return store.ErrOverloaded
	default:
		return &store.PermanentError{Reason: fmt.Sprintf("HTTP %d from %s", resp.StatusCode, path)}
	}
}

// isRelationship reports whether rec is a relationship record by LogicalArtifactType.
func isRelationship(rec store.Record) bool {
	t := strings.ToLower(rec.Schema.LogicalArtifactType)
	return strings.Contains(t, "relationship") || strings.Contains(t, ".rel.")
}

// buildWrappedJSONArray assembles payloads into {"<key>": [...]} body.
// Each payload is already valid canonical JSON.
func buildWrappedJSONArray(key string, recs []store.Record) []byte {
	payloads := make([][]byte, len(recs))
	for i, rec := range recs {
		payloads[i] = rec.Payload
	}
	var buf bytes.Buffer
	buf.WriteString(`{"`)
	buf.WriteString(key)
	buf.WriteString(`":[`)
	buf.Write(bytes.Join(payloads, []byte(",")))
	buf.WriteString("]}")
	return buf.Bytes()
}
