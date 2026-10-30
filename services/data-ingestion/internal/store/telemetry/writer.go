// Package telemetry implements the Telemetry Store writer. It maps batches of
// canonical records to the Telemetry Store ingestion API wire format: NDJSON
// over HTTP with an X-OCG-Label idempotency header derived from the batch ID.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// Config holds the connection settings for the Telemetry Store ingestion API.
type Config struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRecords int
	MaxBytes   int64
	MaxLinger  time.Duration
}

// Writer delivers canonical telemetry records to the Telemetry Store ingestion
// API. It implements store.StoreWriter.
type Writer struct {
	cfg    Config
	client *http.Client
}

var _ store.StoreWriter = (*Writer)(nil)

// New constructs a Writer from cfg.
func New(cfg Config) *Writer {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 500 << 20
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
func (w *Writer) StoreID() store.StoreID { return "telemetry" }

// Limits reports the Telemetry Store's batching thresholds to the drain.
func (w *Writer) Limits() store.BatchLimits {
	return store.BatchLimits{
		MaxRecords: w.cfg.MaxRecords,
		MaxBytes:   w.cfg.MaxBytes,
		MaxLinger:  w.cfg.MaxLinger,
	}
}

// Write delivers batch to the Telemetry Store. Records are grouped by signal
// type and each group is sent as one NDJSON POST. A nil return means the store
// accepted all groups.
func (w *Writer) Write(ctx context.Context, batch store.Batch) error {
	if len(batch.Records) == 0 {
		return nil
	}

	groups, err := groupBySignal(batch.Records)
	if err != nil {
		return err
	}

	// Sort paths so POSTs are issued in a stable order.
	paths := make([]string, 0, len(groups))
	for path := range groups {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		if err := w.post(ctx, path, batch.ID, groups[path]); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) post(ctx context.Context, path string, batchID int64, recs []store.Record) error {
	label := batchLabel(path, batchID)
	body := buildNDJSON(recs)

	url := strings.TrimRight(w.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry writer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-OCG-Label", label)

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: telemetry writer: %w", store.ErrOverloaded, err)
	}
	defer resp.Body.Close()

	return w.handleResponse(resp, label)
}

func (w *Writer) handleResponse(resp *http.Response, label string) error {
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return store.ErrOverloaded
	}
	if resp.StatusCode >= 500 {
		return store.ErrOverloaded
	}
	if resp.StatusCode >= 400 {
		return &store.PermanentError{Reason: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: telemetry writer: read response body: %w", store.ErrOverloaded, err)
	}

	var result storeResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%w: telemetry writer: parse response: %w", store.ErrOverloaded, err)
	}

	if result.FilteredRows > 0 {
		slog.Warn("telemetry writer: store filtered rows — silent data loss",
			"label", label,
			"filtered_rows", result.FilteredRows,
			"error_url", result.ErrorURL,
		)
	}
	return nil
}

type storeResponse struct {
	LoadedRows   int64  `json:"loaded_rows"`
	FilteredRows int64  `json:"filtered_rows"`
	ErrorURL     string `json:"error_url"`
}

func groupBySignal(recs []store.Record) (map[string][]store.Record, error) {
	groups := make(map[string][]store.Record)
	for _, rec := range recs {
		path, err := signalPath(rec.Schema.LogicalArtifactType)
		if err != nil {
			return nil, err
		}
		groups[path] = append(groups[path], rec)
	}
	return groups, nil
}

func signalPath(logicalArtifactType string) (string, error) {
	t := strings.ToLower(logicalArtifactType)
	switch {
	case strings.Contains(t, "log"):
		return "/v1/telemetry/logs", nil
	case strings.Contains(t, "metric"):
		return "/v1/telemetry/metrics", nil
	case strings.Contains(t, "trace"), strings.Contains(t, "span"):
		return "/v1/telemetry/traces", nil
	default:
		return "", &store.PermanentError{Reason: "unknown signal type: " + logicalArtifactType}
	}
}

// batchLabel derives a stable idempotency label from the signal path and the
// batch ID. The batch ID is a monotonic Postgres sequence value that is stable
// across retries and restarts, so the same batch always produces the same label.
func batchLabel(path string, batchID int64) string {
	signal := strings.TrimPrefix(path, "/v1/telemetry/")
	return fmt.Sprintf("%s-%d", signal, batchID)
}

// buildNDJSON encodes recs as NDJSON: one payload per line, no trailing newline.
func buildNDJSON(recs []store.Record) []byte {
	parts := make([][]byte, len(recs))
	for i, rec := range recs {
		parts[i] = rec.Payload
	}
	return bytes.Join(parts, []byte("\n"))
}
