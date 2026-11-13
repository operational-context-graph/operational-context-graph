package envelope

import (
	"encoding/json"
	"fmt"
)

type Message struct {
	LogicalArtifactId string
	SchemaVersion     string
	Payload           json.RawMessage
	Partition         map[string]any
	Watermark         map[string]any
	ContentType       string
}

type idRecord struct {
	ID int `json:"id"`
}

func Build(raw json.RawMessage, endpoint, logicalArtifactType, schemaVersion string) (Message, error) {
	var rec idRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Message{}, fmt.Errorf("envelope: extract id from %s record: %w", endpoint, err)
	}

	return Message{
		LogicalArtifactId: logicalArtifactType,
		SchemaVersion:     schemaVersion,
		Payload:           raw,
		Partition:         map[string]any{"endpoint": endpoint},
		Watermark:         map[string]any{"last_id": rec.ID},
		ContentType:       "application/json",
	}, nil
}
