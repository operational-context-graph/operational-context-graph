// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package schemaregistry provides a client for the schema registry.
package schemaregistry

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// Stub is a no-op implementation of ingest.SchemaRegistryClient for local
// development and integration testing before a real registry is available.
type Stub struct{}

func (s *Stub) Schema(_ context.Context, key ingest.SchemaKey) (ingest.Schema, error) {
	slog.Info("schemaregistry stub: Schema called", "type", key.LogicalArtifactType, "version", key.Version)
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema:any-object", map[string]any{"type": "object"}); err != nil {
		return nil, err
	}
	return c.Compile("schema:any-object")
}

func (s *Stub) SourceMappings(_ context.Context, key ingest.SchemaKey) (ingest.SourceMapping, error) {
	slog.Info("schemaregistry stub: SourceMappings called", "type", key.LogicalArtifactType, "version", key.Version)
	return ingest.SourceMapping{}, nil
}

func (s *Stub) TargetStore(_ context.Context, key ingest.SchemaKey) (ingest.StoreID, error) {
	slog.Info("schemaregistry stub: TargetStore called", "type", key.LogicalArtifactType, "version", key.Version)
	switch key.LogicalArtifactType {
	case "logs", "metrics", "traces":
		return "telemetry", nil
	case "assets", "relationships":
		return "inventory", nil
	default:
		return "", fmt.Errorf("%w: no target store for artifact type %q", ingest.ErrNotFound, key.LogicalArtifactType)
	}
}

func (s *Stub) DefaultLogSchema(_ context.Context) (ingest.SchemaKey, error) {
	slog.Info("schemaregistry stub: DefaultLogSchema called")
	return ingest.SchemaKey{LogicalArtifactType: "logs", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultTraceSchema(_ context.Context) (ingest.SchemaKey, error) {
	slog.Info("schemaregistry stub: DefaultTraceSchema called")
	return ingest.SchemaKey{LogicalArtifactType: "traces", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultMetricSchema(_ context.Context) (ingest.SchemaKey, error) {
	slog.Info("schemaregistry stub: DefaultMetricSchema called")
	return ingest.SchemaKey{LogicalArtifactType: "metrics", Version: "0.1.0"}, nil
}
