// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	v1 "github.com/operational-context-graph/telemetry-store/api/v1"
	"github.com/operational-context-graph/telemetry-store/internal/apperr"
	"github.com/operational-context-graph/telemetry-store/internal/streamload"
)

const (
	routeIngestLogs    = "ingest-logs"
	routeIngestMetrics = "ingest-metrics"

	headerStreamLoadLabel = "X-OCG-Label"
)

// streamLoader is the interface ingest handlers require.
type streamLoader interface {
	Load(ctx context.Context, table, label string, body io.Reader) (*streamload.LoadResult, error)
}

func registerIngestRoutes(rg *gin.RouterGroup, sl streamLoader, maxBodyBytes int64) {
	rg.POST("/logs", setRoute(routeIngestLogs), ndjsonBodyMiddleware(maxBodyBytes), handleIngest(sl, "logs"))
	rg.POST("/metrics", setRoute(routeIngestMetrics), ndjsonBodyMiddleware(maxBodyBytes), handleIngest(sl, "metrics"))
}

// ndjsonBodyMiddleware checks Content-Type and applies a body-size cap.
func ndjsonBodyMiddleware(maxBodyBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		ct := c.GetHeader("Content-Type")
		if ct != "application/x-ndjson" && ct != "application/ndjson" {
			renderError(c, fmt.Errorf("%w: expected application/x-ndjson", apperr.ErrUnsupportedMediaType))
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	}
}

func handleIngest(sl streamLoader, table string) gin.HandlerFunc {
	return func(c *gin.Context) {
		label := c.GetHeader(headerStreamLoadLabel)
		if label == "" {
			renderError(c, fmt.Errorf("%w: %s header is required", apperr.ErrInvalidInput, headerStreamLoadLabel))
			return
		}

		lines, err := readAndValidateNDJSON(c.Request.Body)
		if err != nil {
			renderError(c, err)
			return
		}

		body := bytes.NewReader(lines)
		result, err := sl.Load(c.Request.Context(), table, label, body)
		if err != nil {
			renderError(c, err)
			return
		}

		resp := v1.IngestResponse{
			LoadedRows:   result.NumberLoadedRows,
			FilteredRows: result.NumberFilteredRows,
			ErrorURL:     result.ErrorURL,
		}
		c.JSON(http.StatusOK, resp)
	}
}

// readAndValidateNDJSON reads NDJSON from r, validates the partition-boundary
// constraint (all rows must share the same UTC calendar day), and returns the
// raw bytes to forward to Doris.
func readAndValidateNDJSON(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(r)
	// Allow lines up to 10 MB (large attribute payloads).
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	var partitionDay time.Time
	lineCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		ts, err := extractTimestamp(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %s", apperr.ErrInvalidInput, lineCount+1, err.Error())
		}

		day := ts.UTC().Truncate(24 * time.Hour)
		if lineCount == 0 {
			partitionDay = day
		} else if !day.Equal(partitionDay) {
			return nil, fmt.Errorf(
				"%w: batch spans multiple partitions (%s and %s); flush before the day boundary",
				apperr.ErrInvalidInput,
				partitionDay.Format("2006-01-02"),
				day.Format("2006-01-02"),
			)
		}

		buf.Write(line)
		buf.WriteByte('\n')
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		if mbe, ok := err.(*http.MaxBytesError); ok {
			return nil, mbe
		}
		return nil, fmt.Errorf("%w: reading body: %s", apperr.ErrInvalidInput, err.Error())
	}

	if lineCount == 0 {
		return nil, fmt.Errorf("%w: empty batch", apperr.ErrInvalidInput)
	}

	return buf.Bytes(), nil
}

// extractTimestamp parses only the "timestamp" field from one NDJSON line.
func extractTimestamp(line []byte) (time.Time, error) {
	var row struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(line, &row); err != nil {
		return time.Time{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if row.Timestamp == "" {
		return time.Time{}, fmt.Errorf("missing timestamp field")
	}
	ts, err := time.Parse(time.RFC3339, row.Timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("unparseable timestamp %q", row.Timestamp)
	}
	return ts, nil
}
