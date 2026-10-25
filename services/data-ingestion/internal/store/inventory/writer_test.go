// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package inventory_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store/inventory"
)

func assetRecord(payload string) store.Record {
	return store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "ocg.asset.server", Version: "1.0.0"},
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
}

func relRecord(payload string) store.Record {
	return store.Record{
		Schema:      ingest.SchemaKey{LogicalArtifactType: "canonical_relation", Version: "1.0.0"},
		Payload:     []byte(payload),
		ContentType: "application/json",
	}
}

// relPayload returns a minimal valid relationship JSON payload with source_key
// and target_key arrays so the writer can resolve asset UUIDs.
func relPayload(srcSystem, srcID, tgtSystem, tgtID string) string {
	return `{"artifact_type":"routes_to","source_system":"` + srcSystem + `","source_key":["` + srcSystem + `","` + srcID + `"],"target_key":["` + tgtSystem + `","` + tgtID + `"]}`
}

func batch(id int64, recs ...store.Record) store.Batch {
	return store.Batch{ID: id, Records: recs}
}

type requestLog struct {
	Path string
	Body string
}

func trackingServer(t *testing.T, statusCode int) (*inventory.Writer, *[]requestLog) {
	t.Helper()
	var log []requestLog
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		log = append(log, requestLog{Path: r.URL.Path, Body: string(raw)})
		if r.URL.Path == "/v1/assets" {
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(`{"items":[{"id":"00000000-0000-0000-0000-000000000001"}]}`))
			return
		}
		rw.WriteHeader(statusCode)
	}))
	t.Cleanup(srv.Close)
	w := inventory.New(inventory.Config{BaseURL: srv.URL})
	return w, &log
}

// ── happy path ────────────────────────────────────────────────────────────────

func TestWriter_Write_AssetsOnly(t *testing.T) {
	w, log := trackingServer(t, http.StatusOK)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`), assetRecord(`{"iri":"urn:2"}`)))

	require.NoError(t, err)
	require.Len(t, *log, 1)
	assert.Equal(t, "/v1/assets/batch", (*log)[0].Path)
}

func TestWriter_Write_RelationshipsOnly(t *testing.T) {
	w, log := trackingServer(t, http.StatusOK)

	err := w.Write(context.Background(), batch(1, relRecord(relPayload("netbox", "1", "netbox", "2"))))

	require.NoError(t, err)
	// two asset lookups (GET /v1/assets) + one relationship batch POST
	assert.Equal(t, "/v1/relationships/batch", (*log)[len(*log)-1].Path)
}

func TestWriter_Write_MixedBatch_AssetsFirst(t *testing.T) {
	w, log := trackingServer(t, http.StatusOK)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`), relRecord(relPayload("netbox", "1", "netbox", "2"))))

	require.NoError(t, err)
	// asset batch first, then asset lookups, then relationship batch last
	assert.Equal(t, "/v1/assets/batch", (*log)[0].Path, "assets must be written first")
	assert.Equal(t, "/v1/relationships/batch", (*log)[len(*log)-1].Path, "relationships must be written last")
}

func TestWriter_Write_EmptyBatchIsNoOp(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	w := inventory.New(inventory.Config{BaseURL: srv.URL})

	require.NoError(t, w.Write(context.Background(), store.Batch{ID: 1}))
	assert.False(t, called)
}

// ── wire format ───────────────────────────────────────────────────────────────

func TestWriter_Write_JSONArrayBody(t *testing.T) {
	w, log := trackingServer(t, http.StatusOK)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`), assetRecord(`{"iri":"urn:2"}`)))

	require.NoError(t, err)
	assert.Equal(t, `{"assets":[{"iri":"urn:1"},{"iri":"urn:2"}]}`, (*log)[0].Body)
}

// ── permanent failures ────────────────────────────────────────────────────────

func TestWriter_Write_Asset404_IsPermanentError(t *testing.T) {
	w, _ := trackingServer(t, http.StatusNotFound)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	var pe *store.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestWriter_Write_Relationship404_IsPermanentError(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		callCount++
		switch r.URL.Path {
		case "/v1/assets/batch":
			rw.WriteHeader(http.StatusOK)
		case "/v1/assets":
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(`{"items":[{"id":"00000000-0000-0000-0000-000000000001"}]}`))
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	w := inventory.New(inventory.Config{BaseURL: srv.URL})

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`), relRecord(relPayload("netbox", "1", "netbox", "2"))))

	require.Error(t, err)
	var pe *store.PermanentError
	assert.True(t, errors.As(err, &pe))
}

func TestWriter_Write_Asset400_IsPermanentError(t *testing.T) {
	w, _ := trackingServer(t, http.StatusBadRequest)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	var pe *store.PermanentError
	assert.True(t, errors.As(err, &pe))
}

// ── back-pressure ─────────────────────────────────────────────────────────────

func TestWriter_Write_Asset429_IsOverloaded(t *testing.T) {
	w, _ := trackingServer(t, http.StatusTooManyRequests)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

func TestWriter_Write_Asset503_IsOverloaded(t *testing.T) {
	w, _ := trackingServer(t, http.StatusServiceUnavailable)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

func TestWriter_Write_Asset500_IsOverloaded(t *testing.T) {
	w, _ := trackingServer(t, http.StatusInternalServerError)

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`)))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
}

func TestWriter_Write_AssetFails_RelationshipNotAttempted(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		rw.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	w := inventory.New(inventory.Config{BaseURL: srv.URL})

	err := w.Write(context.Background(), batch(1, assetRecord(`{"iri":"urn:1"}`), relRecord(relPayload("netbox", "1", "netbox", "2"))))

	require.Error(t, err)
	assert.True(t, errors.Is(err, store.ErrOverloaded))
	assert.Equal(t, []string{"/v1/assets/batch"}, paths, "relationship request must not be sent when assets fail")
}

// ── writer metadata ───────────────────────────────────────────────────────────

func TestWriter_StoreID(t *testing.T) {
	w := inventory.New(inventory.Config{BaseURL: "http://localhost"})
	assert.Equal(t, store.StoreID("inventory"), w.StoreID())
}

func TestWriter_Limits(t *testing.T) {
	w := inventory.New(inventory.Config{BaseURL: "http://localhost"})
	limits := w.Limits()
	assert.Equal(t, 1000, limits.MaxRecords)
	assert.Equal(t, int64(0), limits.MaxBytes)
	assert.Equal(t, 30*time.Second, limits.MaxLinger)
}
