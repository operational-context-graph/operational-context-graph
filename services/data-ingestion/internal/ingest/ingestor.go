package ingest

import "context"

// Ingestor is the ingestion pipeline entry point. The receiving layer
// (IngestionService, BackfillService.Ingest, and the OTLP receiver) maps its
// wire format into a batch of IngestRecord and calls Ingest, which runs the
// synchronous ingest half: validate, normalize, and write each record to the
// outbox.
//
// Implementations MUST be safe for concurrent use: Ingest may be called from
// multiple goroutines simultaneously.
type Ingestor interface {
	// Ingest processes one batch and returns per-batch counts. Record-level
	// problems (non-conforming payload, unregistered schema or mapping) are
	// counted in IngestResult, not returned as errors, so one bad record does
	// not stop the others. A non-nil error is a request-level failure (the
	// Schema Registry is unavailable, or the outbox write failed.
	Ingest(ctx context.Context, batch []IngestRecord) (IngestResult, error)
}
