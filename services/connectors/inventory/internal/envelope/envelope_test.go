package envelope

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var deviceRecord = json.RawMessage(`{
	"id": 55147,
	"name": "1010173-sw1",
	"last_updated": "2026-07-10T07:47:31.240892Z",
	"device_type": {"manufacturer": {"name": "Arista"}},
	"role": {"slug": "border-switch"},
	"parent_device": null
}`)

func build(t *testing.T) Message {
	t.Helper()
	msg, err := Build(deviceRecord, "dcim/devices/", "netbox.Device", "0.1.0")
	require.NoError(t, err)
	return msg
}

func TestBuild_SetsFields(t *testing.T) {
	msg := build(t)
	assert.Equal(t, "netbox.Device", msg.LogicalArtifactId)
	assert.Equal(t, "0.1.0", msg.SchemaVersion)
	assert.Equal(t, "application/json", msg.ContentType)
	assert.JSONEq(t, string(deviceRecord), string(msg.Payload))
}

func TestBuild_Partition(t *testing.T) {
	assert.Equal(t, map[string]any{"endpoint": "dcim/devices/"}, build(t).Partition)
}

func TestBuild_Watermark(t *testing.T) {
	assert.Equal(t, map[string]any{"last_id": 55147}, build(t).Watermark)
}

func TestBuild_PayloadIsRaw(t *testing.T) {
	var raw map[string]any
	require.NoError(t, json.Unmarshal(build(t).Payload, &raw))
	assert.Equal(t, float64(55147), raw["id"])
	assert.Equal(t, "1010173-sw1", raw["name"])
}

func TestBuild_MissingID(t *testing.T) {
	msg, err := Build(json.RawMessage(`{"name": "no-id"}`), "dcim/devices/", "netbox.Device", "0.1.0")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"last_id": 0}, msg.Watermark)
}
