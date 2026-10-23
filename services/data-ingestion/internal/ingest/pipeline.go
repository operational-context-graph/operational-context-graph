// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Pipeline is the concrete Ingestor. It runs validate -> normalize -> resolve
// target store per record, then writes the accepted records to the outbox in one
// atomic call.
//
// Pipeline holds no mutable state of its own; all shared state lives in the
// injected dependencies, which are required to be concurrency-safe. Ingest is
// therefore safe for concurrent use without any pipeline-level locking.
type Pipeline struct {
	registry   SchemaRegistry
	normalizer Normalizer
	outbox     OutboxWriter
}

var _ Ingestor = (*Pipeline)(nil)

// NewPipeline builds a Pipeline. All three dependencies MUST be safe for
// concurrent use; Pipeline adds no synchronization of its own.
func NewPipeline(registry SchemaRegistry, normalizer Normalizer, outbox OutboxWriter) *Pipeline {
	return &Pipeline{registry: registry, normalizer: normalizer, outbox: outbox}
}

// Ingest runs the synchronous ingest half for one batch.
func (p *Pipeline) Ingest(ctx context.Context, batch []IngestRecord) (IngestResult, error) {
	if len(batch) == 0 {
		return IngestResult{}, nil
	}

	var result IngestResult
	accepted := make([]OutboxRecord, 0, len(batch))

	for i := range batch {
		rec := batch[i]

		row, ok, err := p.process(ctx, rec)
		if err != nil {
			// Request-level failure: abort the batch so nothing is persisted and
			// the connector retries the whole request.
			return IngestResult{}, err
		}
		if !ok {
			result.Rejected++
			continue
		}
		accepted = append(accepted, row...)
	}

	if len(accepted) > 0 {
		if err := p.outbox.Write(ctx, accepted); err != nil {
			return IngestResult{}, fmt.Errorf("%w: outbox write: %w", ErrUnavailable, err)
		}
	}

	result.Accepted = len(accepted)
	return result, nil
}

// process runs one record through validate -> normalize -> target store.
// It returns (row, true, nil) when the record is accepted, (_, false, nil) when
// the record is rejected (record-level), and (_, false, err) on a request-level
// failure that must abort the batch.
func (p *Pipeline) process(ctx context.Context, rec IngestRecord) ([]OutboxRecord, bool, error) {
	// 1. Fetch schema and validate the payload.
	schema, err := p.registry.Schema(ctx, rec.Schema)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			slog.WarnContext(ctx, "ingest: unregistered schema, record rejected",
				"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version)
			return []OutboxRecord{}, false, nil
		}
		return []OutboxRecord{}, false, fmt.Errorf("%w: schema registry unavailable (schema %s/%s): %w",
			ErrUnavailable, rec.Schema.LogicalArtifactType, rec.Schema.Version, err)
	}

	if !p.validate(ctx, schema, rec) {
		return []OutboxRecord{}, false, nil
	}

	// 2. Fetch the source mapping and normalize to canonical shape.
	mapping, err := p.registry.SourceMappings(ctx, rec.Schema)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			slog.WarnContext(ctx, "ingest: no source mapping, record rejected",
				"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version)
			return []OutboxRecord{}, false, nil
		}
		return []OutboxRecord{}, false, fmt.Errorf("%w: schema registry unavailable (source mappings %s/%s): %w",
			ErrUnavailable, rec.Schema.LogicalArtifactType, rec.Schema.Version, err)
	}

	// The normalization step might return multiple records (e.g. the entity iteself and an edge to a different entity)
	normalized_records, err := p.normalizer.Normalize(ctx, mapping, rec)
	if err != nil {
		slog.WarnContext(ctx, "ingest: normalization failed, record rejected",
			"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version, "error", err)
		return []OutboxRecord{}, false, nil
	}

	// 3. Resolve the target store for each normalized record.
	var rows []OutboxRecord
	for _, normalized := range normalized_records {
		store, err := p.registry.TargetStore(ctx, normalized.Schema)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				slog.WarnContext(ctx, "ingest: no target store, record rejected",
					"logical_artifact_type", normalized.Schema.LogicalArtifactType, "schema_version", normalized.Schema.Version)
				continue
			}
			return []OutboxRecord{}, false, fmt.Errorf("%w: schema registry unavailable (target store %s/%s): %w",
				ErrUnavailable, normalized.Schema.LogicalArtifactType, normalized.Schema.Version, err)
		}
		rows = append(rows, OutboxRecord{
			TargetStore:         store,
			LogicalArtifactType: normalized.Schema.LogicalArtifactType,
			SchemaVersion:       normalized.Schema.Version,
			Payload:             normalized.Payload,
			ContentType:         normalized.ContentType,
		})
	}

	return rows, len(rows) > 0, nil
}

// validate reports whether the record's payload is non-empty, valid JSON, and
// conforms to its schema. A false result is a record-level rejection.
func (p *Pipeline) validate(ctx context.Context, schema Schema, rec IngestRecord) bool {
	if len(rec.Payload) == 0 {
		slog.WarnContext(ctx, "ingest: empty payload, record rejected",
			"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version)
		return false
	}

	var doc any
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(rec.Payload))
	if err != nil {
		slog.WarnContext(ctx, "ingest: payload is not valid JSON, record rejected",
			"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version, "error", err)
		return false
	}

	if err := schema.Validate(doc); err != nil {
		slog.WarnContext(ctx, "ingest: payload failed schema validation, record rejected",
			"logical_artifact_type", rec.Schema.LogicalArtifactType, "schema_version", rec.Schema.Version, "error", err)
		return false
	}

	return true
}
