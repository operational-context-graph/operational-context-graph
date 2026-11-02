package grpcserver

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// SchemaRegistryClient is the subset of the Schema Registry client needed by the
// OTLP receiver. Implementations MUST be safe for concurrent use by multiple goroutines.
type SchemaRegistryClient interface {
	DefaultLogSchema(ctx context.Context) (ingest.SchemaKey, error)
	DefaultMetricSchema(ctx context.Context) (ingest.SchemaKey, error)
	DefaultTraceSchema(ctx context.Context) (ingest.SchemaKey, error)
}
