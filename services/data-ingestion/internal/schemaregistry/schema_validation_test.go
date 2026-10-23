// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package schemaregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/schemaregistry"
)

// TestSchemaValidation loads a sample JSON payload from testdata/<file> and
// validates it against the compiled schema for that artifact type.
// Add a file under testdata/ to enable a case; the test skips if the file is absent.
func TestSchemaValidation(t *testing.T) {
	cases := []struct {
		lat      string
		version  string
		dataFile string
	}{
		// Raw source payloads
		{"netbox.Device", "0.1.0", "netbox_device_chassis.json"},
		{"netbox.Device", "0.1.0", "netbox_device_lb_guest.json"},
		{"generic.log", "0.1.0", "generic_log.json"},
		// Canonical output payloads
		{"canonical_chassis", "0.1.0", "canonical_chassis.json"},
		{"canonical_network_device", "0.1.0", "canonical_network_device.json"},
		{"canonical_lb_guest", "0.1.0", "canonical_lb_guest.json"},
		{"canonical_relation", "0.1.0", "canonical_relation_resides_in.json"},
		{"canonical_relation", "0.1.0", "canonical_relation_hosts.json"},
		{"canonical_octobus_logs", "0.1.0", "canonical_octobus_logs.json"},
	}

	ctx := context.Background()
	s := &schemaregistry.Stub{}

	for _, tc := range cases {
		t.Run(tc.dataFile, func(t *testing.T) {
			path := filepath.Join("testdata", tc.dataFile)
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				t.Skipf("no sample payload at %s — add a file to enable this case", path)
			}
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			schema, err := s.Schema(ctx, ingest.SchemaKey{LogicalArtifactType: tc.lat, Version: tc.version})
			if err != nil {
				t.Fatalf("Schema(%q): %v", tc.lat, err)
			}

			var instance any
			if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&instance); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}

			if err := schema.Validate(instance); err != nil {
				t.Errorf("validation failed for %s:\n%v", path, err)
			}
		})
	}
}
