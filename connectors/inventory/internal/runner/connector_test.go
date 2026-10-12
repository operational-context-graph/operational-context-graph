package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/config"
	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/publisher"
)

type fakeNetboxClient struct {
	records []json.RawMessage
	err     error
}

func (f *fakeNetboxClient) Paginate(_ context.Context, _ string) ([]json.RawMessage, error) {
	return f.records, f.err
}

func newConnector(nb *fakeNetboxClient, artifactTypes map[string]string) *Connector {
	cfg := &config.Config{
		Connector: config.ConnectorConfig{
			SchemaVersion:        "0.1.0",
			LogicalArtifactIds: artifactTypes,
		},
	}
	return New(&publisher.StubPublisher{}, nb, cfg)
}

func TestProcessEndpoint_PublishesRecords(t *testing.T) {
	nb := &fakeNetboxClient{records: []json.RawMessage{
		json.RawMessage(`{"id":1,"name":"device-a"}`),
		json.RawMessage(`{"id":2,"name":"device-b"}`),
	}}
	conn := newConnector(nb, map[string]string{"dcim/devices/": "netbox.Device"})

	result, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/")

	require.NoError(t, err)
	assert.Equal(t, 2, result.Published)
	assert.Equal(t, 0, result.Skipped)
}

func TestProcessEndpoint_SkipsEmptyEndpoint(t *testing.T) {
	conn := newConnector(&fakeNetboxClient{records: nil}, nil)

	result, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/")

	require.NoError(t, err)
	assert.Equal(t, 0, result.Published)
	assert.Equal(t, 1, result.Skipped)
}

func TestProcessEndpoint_DefaultsArtifactType(t *testing.T) {
	nb := &fakeNetboxClient{records: []json.RawMessage{json.RawMessage(`{"id":1}`)}}
	pub := &publisher.StubPublisher{}
	cfg := &config.Config{Connector: config.ConnectorConfig{SchemaVersion: "0.1.0"}}
	conn := New(pub, nb, cfg)

	_, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/")

	require.NoError(t, err)
	assert.Equal(t, "netbox.Unknown", pub.Messages()[0].LogicalArtifactId)
}

func TestProcessEndpoint_PaginateError(t *testing.T) {
	conn := newConnector(&fakeNetboxClient{err: errors.New("timeout")}, nil)

	_, err := conn.ProcessEndpoint(context.Background(), "dcim/devices/")

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
