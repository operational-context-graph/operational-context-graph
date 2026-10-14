// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package schemaregistry provides a client for the schema registry.
package schemaregistry

import (
	"context"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// Stub is a no-op implementation of ingest.SchemaRegistryClient for local
// development and integration testing before a real registry is available.
type Stub struct{}

func (s *Stub) Schema(_ context.Context, _ ingest.SchemaKey) (ingest.Schema, error) {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("any.json", strings.NewReader(`{}`)); err != nil {
		return nil, err
	}
	return c.Compile("any.json")
}

func (s *Stub) SourceMappings(_ context.Context, _ ingest.SchemaKey) (ingest.SourceMapping, error) {
	return ingest.SourceMapping{}, nil
}

func (s *Stub) TargetStore(_ context.Context, _ ingest.SchemaKey) (ingest.StoreID, error) {
	return "stub", nil
}

func (s *Stub) DefaultLogSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{Type: "logs", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultTraceSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{Type: "traces", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultMetricSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{Type: "metrics", Version: "0.1.0"}, nil
}
