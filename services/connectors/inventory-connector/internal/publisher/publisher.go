package publisher

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/envelope"
)

// PublishResult reports what data ingestion did with a publish call. Rejected
// counts the records the server refused synchronously because their type failed a
// gatekeeper (schema registry, normalizer, or target store). The server also
// carries an optional error_message, but it never populates that field today, so
// this struct intentionally omits it.
type PublishResult struct {
	Rejected int
}

type Publisher interface {
	Publish(ctx context.Context, msgs []envelope.Message) (PublishResult, error)
}
