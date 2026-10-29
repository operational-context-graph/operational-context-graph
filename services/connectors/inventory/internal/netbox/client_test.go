package netbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(total int, next *string, ids ...int) []byte {
	results := make([]json.RawMessage, len(ids))
	for idx, id := range ids {
		results[idx] = json.RawMessage(fmt.Sprintf(`{"id":%d,"name":"device-%d","last_updated":"2026-07-10T07:47:31Z"}`, id, id))
	}
	page := pageResponse{Count: total, Next: next, Results: results}
	b, _ := json.Marshal(page)
	return b
}

func TestPaginate_SinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/dcim/devices/", r.URL.Path)
		assert.Equal(t, "1000", r.URL.Query().Get("limit"))
		assert.Equal(t, "0", r.URL.Query().Get("offset"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(2, nil, 1, 2))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", 1000)
	records, err := c.Paginate(context.Background(), "dcim/devices/")
	require.NoError(t, err)
	assert.Len(t, records, 2)
}

func TestPaginate_MultiPage(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			next := srv.URL + "/api/dcim/devices/?limit=2&offset=2"
			_, _ = w.Write(fixture(4, &next, 1, 2))
		} else {
			_, _ = w.Write(fixture(4, nil, 3, 4))
		}
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", 2)
	records, err := c.Paginate(context.Background(), "dcim/devices/")
	require.NoError(t, err)
	assert.Len(t, records, 4)
	assert.Equal(t, 2, calls)
}

func TestPaginate_AuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token secret123", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(1, nil, 1))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "secret123", 1000)
	_, err := c.Paginate(context.Background(), "dcim/devices/")
	require.NoError(t, err)
}

func TestPaginate_EmptyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(0, nil))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", 1000)
	records, err := c.Paginate(context.Background(), "dcim/sites/")
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestPaginate_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", 1000)
	_, err := c.Paginate(context.Background(), "dcim/cable-terminations/")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPaginate_PermanentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", 1000)
	_, err := c.Paginate(context.Background(), "dcim/devices/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
