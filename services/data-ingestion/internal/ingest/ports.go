// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "context"

// SchemaRegistryClient reads immutable schema artifacts from the Schema Registry.
// Results are cacheable because releases are immutable (ADR-012). Lookups take a
// context because they may reach the registry on a cache miss.
//
// Implementations MUST be safe for concurrent use. They MUST wrap a genuine
// not-found (unregistered key; HTTP 404 or equivalent) with ErrNotFound, and MUST
// return any other failure (timeout, connectivity, server error) as a plain error
// so the pipeline treats it as registry-unavailable (request-level).
type SchemaRegistryClient interface {
	Schema(ctx context.Context, key SchemaKey) (Schema, error)
	SourceMappings(ctx context.Context, sourceKey SchemaKey) (SourceMapping, error)
	TargetStore(ctx context.Context, key SchemaKey) (StoreID, error)
	DefaultLogSchema(ctx context.Context) (SchemaKey, error)
	DefaultTraceSchema(ctx context.Context) (SchemaKey, error)
	DefaultMetricSchema(ctx context.Context) (SchemaKey, error)
}

// SourceMappingExecutionEngine applies a source-mapping artifact to a validated
// record and returns the canonical record as an IngestRecord: Payload holds
// canonical JSON and Schema holds the canonical SchemaKey the mapping targets.
// Implementations MUST be safe for concurrent use.
type SourceMappingExecutionEngine interface {
	Normalize(ctx context.Context, mapping SourceMapping, record IngestRecord) (IngestRecord, error)
}

// OutboxWriter persists a batch of normalized records to the durable outbox.
//
// The interface is driver-agnostic by design: no database driver type crosses
// it, so the pipeline and its mocks stay free of the Postgres driver. The
// implementation owns its transaction. Write MUST be atomic: a nil error means
// every row is durable; a non-nil error means no row is visible to any reader.
// Write MUST be a no-op (returning nil) for an empty or nil slice, and MUST be
// safe for concurrent use.
type OutboxWriter interface {
	Write(ctx context.Context, rows []OutboxRecord) error
}
