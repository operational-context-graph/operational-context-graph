package publisher

import (
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
)

const (
	maxMsgsPerBatch  = 1000
	maxBytesPerBatch = 4 * 1024 * 1024
	envelopeOverhead = 256
)

func toProtos(msgs []envelope.Message) ([]*pb.MessageEnvelope, error) {
	out := make([]*pb.MessageEnvelope, len(msgs))
	for idx, msg := range msgs {
		env, err := toProto(msg)
		if err != nil {
			return nil, err
		}
		out[idx] = env
	}
	return out, nil
}

func toProto(msg envelope.Message) (*pb.MessageEnvelope, error) {
	part, err := structpb.NewStruct(msg.Partition)
	if err != nil {
		return nil, err
	}
	wm, err := structpb.NewStruct(msg.Watermark)
	if err != nil {
		return nil, err
	}
	ct := msg.ContentType
	return &pb.MessageEnvelope{
		LogicalArtifactId: msg.LogicalArtifactId,
		SchemaVersion:       msg.SchemaVersion,
		Payload:             msg.Payload,
		Partition:           part,
		Watermark:           wm,
		ContentType:         &ct,
	}, nil
}

func chunk(msgs []envelope.Message) [][]envelope.Message {
	var batches [][]envelope.Message
	var current []envelope.Message
	currentBytes := 0

	for _, msg := range msgs {
		size := len(msg.Payload) + envelopeOverhead
		if len(current) >= maxMsgsPerBatch || (len(current) > 0 && currentBytes+size > maxBytesPerBatch) {
			batches = append(batches, current)
			current = nil
			currentBytes = 0
		}
		current = append(current, msg)
		currentBytes += size
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}
