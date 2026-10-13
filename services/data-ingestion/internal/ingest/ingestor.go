// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "context"

// Ingestor processes a batch of records and reports how many were accepted or
// rejected. An error of ErrOverloaded signals back-pressure; any other non-nil
// error is unexpected and should be treated as an internal failure.
type Ingestor interface {
	Ingest(ctx context.Context, batch []IngestRecord) (IngestResult, error)
}

// NoopIngestor is an Ingestor that accepts every record without doing anything.
// Use it in tests and as a placeholder before the real pipeline is wired.
type NoopIngestor struct{}

// Ingest accepts all records and returns with no error.
func (n *NoopIngestor) Ingest(_ context.Context, batch []IngestRecord) (IngestResult, error) {
	return IngestResult{Accepted: len(batch)}, nil
}

// compile-time interface check
var _ Ingestor = (*NoopIngestor)(nil)
