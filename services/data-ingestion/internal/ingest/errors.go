package ingest

import "errors"

// ErrOverloaded is returned by an Ingestor when the pipeline cannot accept
// more work. Callers MUST use errors.Is to detect it; never match on message text.
var ErrOverloaded = errors.New("pipeline overloaded")

// ErrUnavailable signals a transient infrastructure failure (Schema Registry
// unreachable, outbox write failed). Callers SHOULD retry the full batch.
// Callers MUST use errors.Is to detect it; never match on message text.
var ErrUnavailable = errors.New("service unavailable")
