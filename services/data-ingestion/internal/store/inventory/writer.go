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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		cfg.MaxLinger = 30 * time.Second
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
		resolved, err := w.resolveRelationships(ctx, relationships)
		if err != nil {
			return err
		}
		if len(resolved) > 0 {
			if err := w.postRelationships(ctx, resolved); err != nil {
				return err
			}
		}
	}

	return nil
}

// relPayload is the canonical relationship payload produced by the normalizer.
// source_key and target_key are [source_system, source_id] arrays used to look
// up the asset UUIDs required by the inventory store API.
type relPayload struct {
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceKey           []string        `json:"source_key"`
	TargetKey           []string        `json:"target_key"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
}

// resolvedRel is a relationship body ready to POST to /v1/relationships/batch.
type resolvedRel struct {
	SourceAssetID       string          `json:"source_asset_id"`
	TargetAssetID       string          `json:"target_asset_id"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceKey           string          `json:"source_key"`
	TargetKey           string          `json:"target_key"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
}

// resolveRelationships converts relationship records into resolvedRels by
// looking up source and target asset UUIDs from the inventory store.
// Results are cached within the batch to avoid duplicate requests.
func (w *Writer) resolveRelationships(ctx context.Context, recs []store.Record) ([]resolvedRel, error) {
	cache := make(map[string]string)

	lookup := func(key []string) (string, error) {
		if len(key) != 2 {
			return "", &store.PermanentError{Reason: fmt.Sprintf("inventory writer: invalid key %v: expected [source_system, source_id]", key)}
		}
		cacheKey := key[0] + "\x00" + key[1]
		if id, ok := cache[cacheKey]; ok {
			return id, nil
		}
		id, err := w.lookupAssetID(ctx, key[0], key[1])
		if err != nil {
			return "", err
		}
		cache[cacheKey] = id
		return id, nil
	}

	var out []resolvedRel
	for _, rec := range recs {
		var p relPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return nil, &store.PermanentError{Reason: fmt.Sprintf("inventory writer: unmarshal relationship payload: %v", err)}
		}

		srcID, err := lookup(p.SourceKey)
		if err != nil {
			return nil, err
		}
		tgtID, err := lookup(p.TargetKey)
		if err != nil {
			return nil, err
		}

		out = append(out, resolvedRel{
			SourceAssetID:       srcID,
			TargetAssetID:       tgtID,
			ArtifactType:        p.ArtifactType,
			SourceSystem:        p.SourceSystem,
			SourceKey:           strings.Join(p.SourceKey, ":"),
			TargetKey:           strings.Join(p.TargetKey, ":"),
			Extensions:          p.Extensions,
			IngestedAt:          p.IngestedAt,
			CreatedAtSource:     p.CreatedAtSource,
			LastUpdatedAtSource: p.LastUpdatedAtSource,
		})
	}
	return out, nil
}

// lookupAssetID queries the inventory store for an asset by source_system +
// source_id and returns its UUID string.
func (w *Writer) lookupAssetID(ctx context.Context, sourceSystem, sourceID string) (string, error) {
	u := strings.TrimRight(w.cfg.BaseURL, "/") + "/v1/assets?" + url.Values{
		"source_system": {sourceSystem},
		"source_id":     {sourceID},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("inventory writer: build lookup request: %w", err)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: inventory writer lookup: %w", store.ErrOverloaded, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("%w: inventory writer lookup HTTP %d: %s", store.ErrOverloaded, resp.StatusCode, raw)
	}

	var result struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("inventory writer: decode lookup response: %w", err)
	}
	if len(result.Items) == 0 {
		return "", &store.PermanentError{Reason: fmt.Sprintf("inventory writer: asset not found: %s/%s", sourceSystem, sourceID)}
	}
	return result.Items[0].ID, nil
}

// postRelationships POSTs resolved relationships to /v1/relationships/batch.
func (w *Writer) postRelationships(ctx context.Context, rels []resolvedRel) error {
	body, err := json.Marshal(struct {
		Relationships []resolvedRel `json:"relationships"`
	}{Relationships: rels})
	if err != nil {
		return fmt.Errorf("inventory writer: marshal relationships: %w", err)
	}

	u := strings.TrimRight(w.cfg.BaseURL, "/") + "/v1/relationships/batch"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("inventory writer: build relationships request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: inventory writer relationships: %w", store.ErrOverloaded, err)
	}
	defer resp.Body.Close()

	return w.handleResponse(resp, "/v1/relationships/batch")
}

func (w *Writer) postBatch(ctx context.Context, path, key string, recs []store.Record) error {
	body := buildWrappedJSONArray(key, recs)

	u := strings.TrimRight(w.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
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
	return strings.Contains(t, "relation") || strings.Contains(t, ".rel.")
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
