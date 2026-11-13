package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is returned when NetBox responds with 404 for an endpoint.
var ErrNotFound = errors.New("netbox: endpoint not found")

type pageResponse struct {
	Count   int               `json:"count"`
	Next    *string           `json:"next"`
	Results []json.RawMessage `json:"results"`
}

// Client paginates a NetBox endpoint and returns all records.
type Client interface {
	Paginate(ctx context.Context, path string) ([]json.RawMessage, error)
}

type HTTPClient struct {
	base     string
	pageSize int
	http     *http.Client
}

func NewHTTPClient(baseURL, token string, pageSize int) *HTTPClient {
	return &HTTPClient{
		base:     baseURL,
		pageSize: pageSize,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &authTransport{token: token, inner: http.DefaultTransport},
		},
	}
}

func (c *HTTPClient) Paginate(ctx context.Context, path string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	offset := 0

	for {
		url := fmt.Sprintf("%s/api/%s?limit=%d&offset=%d", c.base, path, c.pageSize, offset)
		page, err := c.fetchPage(ctx, url)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if page.Next == nil {
			return all, nil
		}
		offset += len(page.Results)
	}
}

func (c *HTTPClient) fetchPage(ctx context.Context, url string) (*pageResponse, error) {
	const maxAttempts = 5

	for attempt := range maxAttempts {
		page, retry, err := c.doRequest(ctx, url)
		if err == nil {
			return page, nil
		}
		if !retry || attempt == maxAttempts-1 {
			return nil, err
		}
		sleep(ctx, backoff(attempt))
	}
	return nil, fmt.Errorf("netbox: exhausted retries for %s", url)
}

func (c *HTTPClient) doRequest(ctx context.Context, url string) (*pageResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, isRetryableErr(err), err
	}
	defer func() { _ = resp.Body.Close() }() // Body.Close errors are non-actionable in defers.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return nil, false, ErrNotFound
		}
		return nil, isRetryableStatus(resp.StatusCode), fmt.Errorf("netbox: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	var page pageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, false, fmt.Errorf("netbox: decode: %w", err)
	}
	return &page, false, nil
}
