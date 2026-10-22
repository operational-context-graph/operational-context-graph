// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package schemaregistry provides a self-contained implementation of
// ingest.SchemaRegistry that loads and compiles the bundled JSON Schema files
// once on first use.
package schemaregistry

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

// schemaEntry binds an embedded file to its SchemaKey lookup values.
type schemaEntry struct {
	file                string
	logicalArtifactType string
	version             string
}

var registeredSchemas = []schemaEntry{
	{"schemas/canonical_chassis.json", "canonical_chassis", "0.1.0"},
	{"schemas/canonical_network_device.json", "canonical_network_device", "0.1.0"},
	{"schemas/canonical_lb_guest.json", "canonical_lb_guest", "0.1.0"},
	{"schemas/canonical_relation.json", "canonical_relation", "0.1.0"},
	{"schemas/canonical_octobus_log.json", "canonical_octobus_logs", "0.1.0"},
	{"schemas/device.json", "netbox.device", "0.1.0"},
	{"schemas/log.json", "generic.log", "0.1.0"},
}

// Stub implements ingest.SchemaRegistry using JSON Schema files bundled at
// compile time. All schemas are compiled once on first use.
type Stub struct {
	once    sync.Once
	schemas map[ingest.SchemaKey]ingest.Schema
	initErr error
}

func (s *Stub) init() {
	s.once.Do(func() {
		s.schemas, s.initErr = compileAll()
	})
}

func compileAll() (map[ingest.SchemaKey]ingest.Schema, error) {
	c := jsonschema.NewCompiler()

	type entry struct {
		key ingest.SchemaKey
		id  string
	}
	entries := make([]entry, 0, len(registeredSchemas))

	// First pass: register all resources so cross-schema $refs resolve.
	for _, e := range registeredSchemas {
		data, err := schemaFiles.ReadFile(e.file)
		if err != nil {
			return nil, fmt.Errorf("schemaregistry: read %s: %w", e.file, err)
		}
		var raw map[string]any
		if err := json.NewDecoder(bytes.NewReader(data)).Decode(&raw); err != nil {
			return nil, fmt.Errorf("schemaregistry: decode %s: %w", e.file, err)
		}
		id, _ := raw["$id"].(string)
		if id == "" {
			id = "urn:ocg:schema:" + e.logicalArtifactType + ":" + e.version
		}
		if err := c.AddResource(id, raw); err != nil {
			return nil, fmt.Errorf("schemaregistry: add resource %s: %w", id, err)
		}
		entries = append(entries, entry{
			key: ingest.SchemaKey{LogicalArtifactType: e.logicalArtifactType, Version: e.version},
			id:  id,
		})
	}

	// Second pass: compile and index by SchemaKey.
	out := make(map[ingest.SchemaKey]ingest.Schema, len(entries))
	for _, e := range entries {
		compiled, err := c.Compile(e.id)
		if err != nil {
			return nil, fmt.Errorf("schemaregistry: compile %s: %w", e.id, err)
		}
		out[e.key] = compiled
	}
	return out, nil
}

func (s *Stub) Schema(_ context.Context, key ingest.SchemaKey) (ingest.Schema, error) {
	s.init()
	if s.initErr != nil {
		return nil, s.initErr
	}
	schema, ok := s.schemas[key]
	if !ok {
		return nil, fmt.Errorf("%w: schema %q %q", ingest.ErrNotFound, key.LogicalArtifactType, key.Version)
	}
	return schema, nil
}

func (s *Stub) SourceMappings(_ context.Context, _ ingest.SchemaKey) (ingest.SourceMapping, error) {
	return ingest.SourceMapping{}, nil
}

func (s *Stub) TargetStore(_ context.Context, key ingest.SchemaKey) (ingest.StoreID, error) {
	switch {
	case key.LogicalArtifactType == "canonical_octobus_logs":
		return "telemetry", nil
	case strings.HasPrefix(key.LogicalArtifactType, "canonical_"):
		return "inventory", nil
	default:
		return "", fmt.Errorf("%w: no target store for %q", ingest.ErrNotFound, key.LogicalArtifactType)
	}
}

func (s *Stub) DefaultLogSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{LogicalArtifactType: "generic.log", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultTraceSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{LogicalArtifactType: "traces", Version: "0.1.0"}, nil
}

func (s *Stub) DefaultMetricSchema(_ context.Context) (ingest.SchemaKey, error) {
	return ingest.SchemaKey{LogicalArtifactType: "metrics", Version: "0.1.0"}, nil
}
