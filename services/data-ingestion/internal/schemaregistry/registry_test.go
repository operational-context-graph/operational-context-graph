package schemaregistry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/schemaregistry"
)

func newStub() *schemaregistry.Stub { return &schemaregistry.Stub{} }

func TestSchema_knownKeys(t *testing.T) {
	ctx := context.Background()
	s := newStub()
	for _, tc := range []struct {
		lat     string
		version string
	}{
		{"canonical_chassis", "0.1.0"},
		{"canonical_network_device", "0.1.0"},
		{"canonical_lb_guest", "0.1.0"},
		{"canonical_relation", "0.1.0"},
		{"canonical_octobus_logs", "0.1.0"},
		{"netbox.Device", "0.1.0"},
		{"generic.log", "0.1.0"},
	} {
		key := ingest.SchemaKey{LogicalArtifactType: tc.lat, Version: tc.version}
		schema, err := s.Schema(ctx, key)
		if err != nil {
			t.Errorf("Schema(%q %q): unexpected error: %v", tc.lat, tc.version, err)
			continue
		}
		if schema == nil {
			t.Errorf("Schema(%q %q): got nil schema", tc.lat, tc.version)
		}
	}
}

func TestSchema_unknownKey(t *testing.T) {
	ctx := context.Background()
	s := newStub()
	_, err := s.Schema(ctx, ingest.SchemaKey{LogicalArtifactType: "unknown.type", Version: "0.1.0"})
	if !errors.Is(err, ingest.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestTargetStore(t *testing.T) {
	ctx := context.Background()
	s := newStub()
	for _, tc := range []struct {
		lat  string
		want ingest.StoreID
	}{
		{"canonical_octobus_logs", "telemetry"},
		{"canonical_chassis", "inventory"},
		{"canonical_network_device", "inventory"},
		{"canonical_lb_guest", "inventory"},
		{"canonical_relation", "inventory"},
	} {
		got, err := s.TargetStore(ctx, ingest.SchemaKey{LogicalArtifactType: tc.lat, Version: "0.1.0"})
		if err != nil {
			t.Errorf("TargetStore(%q): unexpected error: %v", tc.lat, err)
			continue
		}
		if got != tc.want {
			t.Errorf("TargetStore(%q) = %q, want %q", tc.lat, got, tc.want)
		}
	}
}

func TestTargetStore_nonCanonicalErrors(t *testing.T) {
	ctx := context.Background()
	s := newStub()
	for _, lat := range []string{"netbox.Device", "generic.log", "unknown.type"} {
		_, err := s.TargetStore(ctx, ingest.SchemaKey{LogicalArtifactType: lat, Version: "0.1.0"})
		if !errors.Is(err, ingest.ErrNotFound) {
			t.Errorf("TargetStore(%q): want ErrNotFound, got %v", lat, err)
		}
	}
}
