package publisher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/envelope"
)

func msg(id int, payloadSize int) envelope.Message {
	return envelope.Message{
		LogicalArtifactId: "netbox.Device",
		SchemaVersion:       "0.1.0",
		Payload:             json.RawMessage(`{"id":` + string(rune('0'+id)) + `}`),
		Partition:           map[string]any{"endpoint": "dcim/devices/"},
		Watermark:           map[string]any{"last_id": id},
		ContentType:         "application/json",
		// pad payload to requested size
	}
}

func msgWithPayload(size int) envelope.Message {
	return envelope.Message{
		LogicalArtifactId: "netbox.Device",
		SchemaVersion:       "0.1.0",
		Payload:             json.RawMessage(`"` + strings.Repeat("x", size) + `"`),
		Partition:           map[string]any{"endpoint": "dcim/devices/"},
		Watermark:           map[string]any{"last_id": 1},
		ContentType:         "application/json",
	}
}

func TestChunk_SingleBatch(t *testing.T) {
	msgs := make([]envelope.Message, 10)
	for i := range msgs {
		msgs[i] = msg(i, 0)
	}
	batches := chunk(msgs)
	assert.Len(t, batches, 1)
	assert.Len(t, batches[0], 10)
}

func TestChunk_SplitsOnMsgCount(t *testing.T) {
	msgs := make([]envelope.Message, maxMsgsPerBatch+1)
	for i := range msgs {
		msgs[i] = msg(i%10, 0)
	}
	batches := chunk(msgs)
	assert.Len(t, batches, 2)
	assert.Len(t, batches[0], maxMsgsPerBatch)
	assert.Len(t, batches[1], 1)
}

func TestChunk_SplitsOnBytes(t *testing.T) {
	// each message payload ~2 MiB — two should exceed 4 MiB limit
	big := msgWithPayload(2*1024*1024 - envelopeOverhead)
	batches := chunk([]envelope.Message{big, big, big})
	assert.Len(t, batches, 3)
}

func TestToProto_Fields(t *testing.T) {
	m := envelope.Message{
		LogicalArtifactId: "netbox.Device",
		SchemaVersion:       "0.1.0",
		Payload:             json.RawMessage(`{"id":1}`),
		Partition:           map[string]any{"endpoint": "dcim/devices/"},
		Watermark:           map[string]any{"last_id": 1},
		ContentType:         "application/json",
	}
	env, err := toProto(m)
	require.NoError(t, err)
	assert.Equal(t, "netbox.Device", env.LogicalArtifactId)
	assert.Equal(t, "0.1.0", env.SchemaVersion)
	assert.Equal(t, []byte(`{"id":1}`), env.Payload)
	assert.Equal(t, "application/json", env.GetContentType())
	assert.Equal(t, "dcim/devices/", env.Partition.Fields["endpoint"].GetStringValue())
	assert.Equal(t, float64(1), env.Watermark.Fields["last_id"].GetNumberValue())
}
