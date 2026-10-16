// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package sourcemapping provides the source mapping execution engine.
package sourcemapping

import (
	"context"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// Stub is a no-op implementation of ingest.SourceMappingExecutionEngine for
// local development and integration testing before a real engine is available.
type Stub struct{}

func (s *Stub) Normalize(_ context.Context, _ ingest.SourceMapping, record ingest.IngestRecord) (ingest.IngestRecord, error) {
	return record, nil
}
