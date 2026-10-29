package ingest

import "context"

// Normalizer applies a source-mapping artifact to a validated record and returns
// the canonical record as an IngestRecord: Payload holds canonical JSON and Schema
// holds the canonical SchemaKey the mapping targets.
// Implementations MUST be safe for concurrent use.
type Normalizer interface {
	Normalize(ctx context.Context, mapping SourceMapping, record IngestRecord) ([]IngestRecord, error)
}
