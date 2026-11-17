package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/backfill"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/netbox"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/publisher"
)

type fakeNetboxClient struct {
	records    []json.RawMessage
	err        error
	lastParams url.Values
}

func (f *fakeNetboxClient) Paginate(_ context.Context, _ string, params url.Values) ([]json.RawMessage, error) {
	f.lastParams = params
	return f.records, f.err
}

func newConnector(nb *fakeNetboxClient, artifactTypes map[string]string) *Connector {
	cfg := &config.Config{
		SchemaVersion: "0.1.0",
		ArtifactTypes: artifactTypes,
	}
	return New(&publisher.StubPublisher{}, nb, &backfill.StubBackfillClient{}, cfg)
}

func TestProcessEndpoint_PublishesRecords(t *testing.T) {
	nb := &fakeNetboxClient{records: []json.RawMessage{
		json.RawMessage(`{"id":1,"name":"device-a"}`),
		json.RawMessage(`{"id":2,"name":"device-b"}`),
	}}
	conn := newConnector(nb, map[string]string{"dcim/devices/": "netbox.Device"})

	result, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/", nil)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Published)
	assert.Equal(t, 0, result.Skipped)
}

func TestProcessEndpoint_SkipsEmptyEndpoint(t *testing.T) {
	conn := newConnector(&fakeNetboxClient{records: nil}, nil)

	result, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/", nil)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Published)
	assert.Equal(t, 1, result.Skipped)
}

func TestProcessEndpoint_DefaultsArtifactType(t *testing.T) {
	nb := &fakeNetboxClient{records: []json.RawMessage{json.RawMessage(`{"id":1}`)}}
	pub := &publisher.StubPublisher{}
	cfg := &config.Config{SchemaVersion: "0.1.0"}
	conn := New(pub, nb, &backfill.StubBackfillClient{}, cfg)

	_, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/", nil)

	require.NoError(t, err)
	assert.Equal(t, "netbox.Device", pub.Messages()[0].LogicalArtifactId)
}

func TestProcessEndpoint_SkipsNotFound(t *testing.T) {
	conn := newConnector(&fakeNetboxClient{err: netbox.ErrNotFound}, nil)

	result, err := conn.ProcessEndpoint(context.Background(), "dcim/cable-terminations/", nil)

	require.NoError(t, err)
	assert.Equal(t, 0, result.Published)
	assert.Equal(t, 1, result.Skipped)
}

func TestProcessEndpoint_PaginateError(t *testing.T) {
	conn := newConnector(&fakeNetboxClient{err: errors.New("timeout")}, nil)

	_, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/", nil)

	assert.ErrorContains(t, err, "timeout")
}

func TestBuildEnvelopes_SkipsMalformed(t *testing.T) {
	conn := newConnector(nil, map[string]string{"dcim/devices/": "netbox.Device"})
	records := []json.RawMessage{
		json.RawMessage(`{"id":1}`),
		json.RawMessage(`not-json`),
		json.RawMessage(`{"id":3}`),
	}

	messages := conn.buildEnvelopes("dcim/devices/", records)

	assert.Len(t, messages, 2)
	assert.Equal(t, "netbox.Device", messages[0].LogicalArtifactId)
	assert.Equal(t, "netbox.Device", messages[1].LogicalArtifactId)
}

func runWithWatermark(t *testing.T, stub *backfill.StubBackfillClient) (*fakeNetboxClient, Result, error) {
	t.Helper()
	nb := &fakeNetboxClient{records: []json.RawMessage{json.RawMessage(`{"id":7,"last_updated":"2026-07-10T07:47:31Z"}`)}}
	cfg := &config.Config{
		SchemaVersion: "0.1.0",
		Endpoints:     []string{"dcim/devices/"},
		ArtifactTypes: map[string]string{"dcim/devices/": "netbox.Device"},
	}
	conn := New(&publisher.StubPublisher{}, nb, stub, cfg)
	result, err := conn.Run(context.Background())
	return nb, result, err
}

func TestRun_WatermarkPresent_QueriesPartitionAndPublishes(t *testing.T) {
	stub := &backfill.StubBackfillClient{Watermark: map[string]any{"last_updated": "2026-07-10T07:47:31Z"}}

	nb, result, err := runWithWatermark(t, stub)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Published)
	require.Len(t, stub.Partitions, 1)
	assert.Equal(t, map[string]any{"endpoint": "dcim/devices/"}, stub.Partitions[0])
	assert.Equal(t, "2026-07-10T07:47:31Z", nb.lastParams.Get("last_updated__gte"))
	assert.Equal(t, "last_updated", nb.lastParams.Get("ordering"))
}

func TestRun_WatermarkAbsent_StillPublishes(t *testing.T) {
	stub := &backfill.StubBackfillClient{Watermark: nil}

	nb, result, err := runWithWatermark(t, stub)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Published)
	assert.Len(t, stub.Partitions, 1)
	assert.Nil(t, nb.lastParams)
}

func TestRun_WatermarkError_WarnsAndContinues(t *testing.T) {
	stub := &backfill.StubBackfillClient{Err: errors.New("ingestion unavailable")}

	nb, result, err := runWithWatermark(t, stub)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Published)
	assert.Len(t, stub.Partitions, 1)
	assert.Nil(t, nb.lastParams)
}
