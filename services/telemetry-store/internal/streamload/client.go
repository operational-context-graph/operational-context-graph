// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package streamload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/operational-context-graph/telemetry-store/internal/apperr"
)

// LoadResult holds the parsed Doris Stream Load response fields callers need.
type LoadResult struct {
	TxnID              int64
	Label              string
	NumberLoadedRows   int64
	NumberFilteredRows int64
	ErrorURL           string
}

// Client sends NDJSON batches to Doris via the Stream Load HTTP API.
type Client struct {
	baseURL    string
	authHeader string
	http       *http.Client
}

// NewClient creates a Client targeting the given Doris FE base URL.
// baseURL must be the FE HTTP address, e.g. "http://doris-fe:8030".
func NewClient(baseURL, user, password string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password)),
		http: &http.Client{
			Timeout: 10 * time.Minute,
			// Do not follow 307 automatically — we re-attach auth on the redirect.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Load sends body as an NDJSON Stream Load to the given Doris table.
// table must be one of "logs", "metrics", or "traces".
// label is the idempotency key; it must be unique per batch and stable across retries.
func (c *Client) Load(ctx context.Context, table, label string, body io.Reader) (*LoadResult, error) {
	url := fmt.Sprintf("%s/api/ocg/%s/_stream_load", c.baseURL, table)
	return c.doLoad(ctx, url, label, body)
}

func (c *Client) doLoad(ctx context.Context, url, label string, body io.Reader) (*LoadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return nil, fmt.Errorf("build stream load request: %w", err)
	}
	c.setHeaders(req, label)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", apperr.ErrDorisOverloaded, err.Error())
	}
	defer resp.Body.Close()

	// Doris FE redirects to a coordinator BE on 307; re-attach auth.
	if resp.StatusCode == http.StatusTemporaryRedirect {
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("%w: 307 without Location header", apperr.ErrDorisLoadFailed)
		}
		_, _ = io.Copy(io.Discard, resp.Body)

		req2, err := http.NewRequestWithContext(ctx, http.MethodPut, location, body)
		if err != nil {
			return nil, fmt.Errorf("build redirect request: %w", err)
		}
		c.setHeaders(req2, label)
		resp, err = c.http.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", apperr.ErrDorisOverloaded, err.Error())
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode == http.StatusServiceUnavailable {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, apperr.ErrDorisOverloaded
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read stream load response: %w", err)
	}

	var sr streamLoadResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, fmt.Errorf("parse stream load response: %w", err)
	}

	return sr.toResult()
}

func (c *Client) setHeaders(req *http.Request, label string) {
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("format", "json")
	req.Header.Set("read_json_by_line", "true")
	req.Header.Set("strip_outer_array", "false")
	// load_to_single_tablet is not set: HASH-bucketed Unique Key tables do not support it (SDD §4.2).
	req.Header.Set("label", label)
}

// streamLoadResponse is the JSON body Doris always returns with HTTP 200.
type streamLoadResponse struct {
	TxnID              int64  `json:"TxnId"`
	Label              string `json:"Label"`
	Status             string `json:"Status"`
	NumberLoadedRows   int64  `json:"NumberLoadedRows"`
	NumberFilteredRows int64  `json:"NumberFilteredRows"`
	ErrorURL           string `json:"ErrorURL"`
}

func (r *streamLoadResponse) toResult() (*LoadResult, error) {
	switch r.Status {
	case "Success", "Publish Timeout":
		return &LoadResult{
			TxnID:              r.TxnID,
			Label:              r.Label,
			NumberLoadedRows:   r.NumberLoadedRows,
			NumberFilteredRows: r.NumberFilteredRows,
			ErrorURL:           r.ErrorURL,
		}, nil
	case "Label Already Exists":
		return nil, apperr.ErrLabelExists
	case "Fail":
		// -235 signals BE memory pressure (compaction backlog).
		if strings.Contains(r.ErrorURL, "-235") {
			return nil, apperr.ErrDorisOverloaded
		}
		return nil, fmt.Errorf("%w: label=%s", apperr.ErrDorisLoadFailed, r.Label)
	default:
		return nil, fmt.Errorf("%w: unexpected status %q", apperr.ErrDorisLoadFailed, r.Status)
	}
}
