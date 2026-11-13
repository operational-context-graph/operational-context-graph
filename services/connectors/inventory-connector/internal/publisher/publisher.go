package publisher

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/envelope"
)

type Publisher interface {
	Publish(ctx context.Context, msgs []envelope.Message) error
}
