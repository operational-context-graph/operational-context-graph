// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "context"

// SchemaRegistry reads immutable schema artifacts from the Schema Registry.
// Results are cacheable because releases are immutable (ADR-012). Lookups take a
// context because they may reach the registry on a cache miss.
//
// Implementations MUST be safe for concurrent use. They MUST wrap a genuine
// not-found (unregistered key; HTTP 404 or equivalent) with ErrNotFound, and MUST
// return any other failure (timeout, connectivity, server error) as a plain error
// so the pipeline treats it as registry-unavailable (request-level).
type SchemaRegistry interface {
	Schema(ctx context.Context, key SchemaKey) (Schema, error)
	SourceMappings(ctx context.Context, sourceKey SchemaKey) (SourceMapping, error)
	TargetStore(ctx context.Context, key SchemaKey) (StoreID, error)
	DefaultLogSchema(ctx context.Context) (SchemaKey, error)
	DefaultTraceSchema(ctx context.Context) (SchemaKey, error)
	DefaultMetricSchema(ctx context.Context) (SchemaKey, error)
}
