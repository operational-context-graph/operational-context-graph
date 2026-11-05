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

type tsRecord struct {
	LastUpdated string `json:"last_updated"`
}

func Build(raw json.RawMessage, endpoint, logicalArtifactType, schemaVersion string) (Message, error) {
	var rec tsRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Message{}, fmt.Errorf("envelope: extract last_updated from %s record: %w", endpoint, err)
	}

	return Message{
		LogicalArtifactId: logicalArtifactType,
		SchemaVersion:     schemaVersion,
		Payload:           raw,
		Partition:         map[string]any{"endpoint": endpoint},
		Watermark:         map[string]any{"last_updated": rec.LastUpdated},
		ContentType:       "application/json",
	}, nil
}
