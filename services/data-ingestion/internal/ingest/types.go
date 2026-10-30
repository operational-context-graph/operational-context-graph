// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package ingest holds the synchronous ingest pipeline and the domain types and
// interfaces it depends on. It is wire-neutral: receivers map their protocol
// (OTLP, gRPC) into IngestRecord batches and call Ingestor.
package ingest

import (
	"errors"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrNotFound classifies a registry lookup whose key does not exist (unregistered
// schema, source mapping, or target-store entry). The pipeline treats an error
// satisfying errors.Is(err, ErrNotFound) as a record-level rejection; any other
// error from the registry is a request-level failure (the registry is
// unavailable) and makes the connector retry. SchemaRegistry implementations
// MUST wrap a genuine not-found with this sentinel and return
// anything else (timeout, connectivity, server error) as a plain error.
var ErrNotFound = errors.New("not found")

// Schema is a compiled JSON Schema (Draft 2020-12) returned by the Schema
// Registry client. Compiling once in the client keeps parsing out of the
// per-record hot path.
type Schema = *jsonschema.Schema

// SchemaKey identifies one immutable schema release: the (logical_artifact_type,
// schema_version) pair. LogicalArtifactType is the stable logical artifact type;
// Version is the global semantic schema version. Passing it as one value keeps
// call sites type-safe and prevents swapping the two strings.
type SchemaKey struct {
	LogicalArtifactType string
	Version             string
}

// StoreID is the destination store a record is routed to. The same vocabulary is
// used by the Schema Registry's target-store mapping and the outbox target_store
// column.
type StoreID string

// IngestRecord is the wire-neutral record the pipeline consumes and, after
// normalization, produces. On input Payload is the source JSON and Schema is the
// source key; after Normalizer.Normalize, Payload is canonical
// JSON and Schema is the canonical key.
type IngestRecord struct {
	Schema      SchemaKey
	Payload     []byte
	ContentType string
	Partition   string         // optional, opaque; empty for OTLP
	Watermark   map[string]any // optional, opaque; empty for OTLP
}

// IngestResult summarises the outcome of a batch ingest call.
// Accepted has one entry per input record in the same order: true means the
// record was accepted and written to the outbox, false means it was rejected.
// AcceptedCount and RejectedCount are convenience totals derived from Accepted.
type IngestResult struct {
	AcceptedCount int
	RejectedCount int
	Accepted      []bool
}

// SourceMapping is the source-mapping artifact fetched from the Schema Registry.
// The pipeline treats it as opaque: it passes the value from SourceMappings to
// Normalize without reading its fields. The Source Mapping Execution Engine
// (PR #99) owns its contents.
type SourceMapping struct {
	// Opaque to the pipeline; defined by the Source Mapping Execution Engine.
}

// OutboxRecord is one row the pipeline hands to the OutboxWriter after
// normalization. id and created_at are set by the database; batch_id starts NULL.
type OutboxRecord struct {
	TargetStore         StoreID
	LogicalArtifactType string
	SchemaVersion       string
	Payload             []byte
	ContentType         string
}
