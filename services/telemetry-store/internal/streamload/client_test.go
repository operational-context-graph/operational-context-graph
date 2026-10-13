// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package streamload_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/operational-context-graph/telemetry-store/internal/apperr"
	"github.com/operational-context-graph/telemetry-store/internal/streamload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validNDJSON = `{"timestamp":"2026-10-13T10:00:00Z","host":"h","severity":"INFO","body":"ok"}` + "\n"

func dorisResponse(status string, loadedRows, filteredRows int, errorURL string) string {
	return `{"TxnId":1,"Label":"test-label","Status":"` + status + `","NumberLoadedRows":` +
		itoa(loadedRows) + `,"NumberFilteredRows":` + itoa(filteredRows) + `,"ErrorURL":"` + errorURL + `"}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func newFakeDoris(statusCode int, body string) (*httptest.Server, *streamload.Client) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	client := streamload.NewClient(srv.URL, "user", "pass")
	return srv, client
}

func TestLoad_Success(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Success", 5, 0, ""))
	defer srv.Close()

	result, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	require.NoError(t, err)
	assert.Equal(t, int64(5), result.NumberLoadedRows)
	assert.Equal(t, int64(0), result.NumberFilteredRows)
}

func TestLoad_PublishTimeout_TreatedAsSuccess(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Publish Timeout", 3, 0, ""))
	defer srv.Close()

	result, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.NumberLoadedRows)
}

func TestLoad_LabelAlreadyExists(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Label Already Exists", 0, 0, ""))
	defer srv.Close()

	_, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	assert.ErrorIs(t, err, apperr.ErrLabelExists)
}

func TestLoad_Fail(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Fail", 0, 0, "http://be/error"))
	defer srv.Close()

	_, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	assert.ErrorIs(t, err, apperr.ErrDorisLoadFailed)
}

func TestLoad_Fail_With235_ReturnsOverloaded(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Fail", 0, 0, "http://be/error?code=-235"))
	defer srv.Close()

	_, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	assert.ErrorIs(t, err, apperr.ErrDorisOverloaded)
}

func TestLoad_HTTP503_ReturnsOverloaded(t *testing.T) {
	srv, client := newFakeDoris(http.StatusServiceUnavailable, "")
	defer srv.Close()

	_, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	assert.ErrorIs(t, err, apperr.ErrDorisOverloaded)
}

func TestLoad_FilteredRows_Populated(t *testing.T) {
	srv, client := newFakeDoris(http.StatusOK, dorisResponse("Success", 97, 3, "http://be/errors"))
	defer srv.Close()

	result, err := client.Load(context.Background(), "logs", "test-label", strings.NewReader(validNDJSON))
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.NumberFilteredRows)
	assert.Equal(t, "http://be/errors", result.ErrorURL)
}

func TestLoad_SetsCorrectHeaders(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dorisResponse("Success", 1, 0, "")))
	}))
	defer srv.Close()

	client := streamload.NewClient(srv.URL, "myuser", "mypass")
	_, err := client.Load(context.Background(), "logs", "my-label", strings.NewReader(validNDJSON))
	require.NoError(t, err)

	assert.Equal(t, "json", capturedReq.Header.Get("format"))
	assert.Equal(t, "true", capturedReq.Header.Get("read_json_by_line"))
	assert.Equal(t, "false", capturedReq.Header.Get("strip_outer_array"))
	assert.Empty(t, capturedReq.Header.Get("load_to_single_tablet"), "must not be set on HASH-bucketed tables")
	assert.Equal(t, "my-label", capturedReq.Header.Get("label"))
	assert.Contains(t, capturedReq.Header.Get("Authorization"), "Basic ")
}
