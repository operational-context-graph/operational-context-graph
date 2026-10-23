// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package sourcemapping_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/sourcemapping"
)

var update = flag.Bool("update", false, "regenerate golden files in testdata/")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

const inputBase = "../schemaregistry/testdata/"

// normalize reads a raw input file and calls Stub.Normalize.
func normalize(t *testing.T, schemaType, file string) []ingest.IngestRecord {
	t.Helper()
	payload, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	stub := &sourcemapping.Stub{}
	records, err := stub.Normalize(context.Background(), ingest.SourceMapping{}, ingest.IngestRecord{
		Schema:  ingest.SchemaKey{LogicalArtifactType: schemaType, Version: "0.1.0"},
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("Normalize(%s): %v", file, err)
	}
	return records
}

// assertGolden compares actual JSON payload against a golden file.
// Run with -update to regenerate the golden file from current output.
func assertGolden(t *testing.T, actual []byte, goldenPath string) {
	t.Helper()

	// Normalise: unmarshal then re-marshal with sorted keys and indent.
	var anyVal any
	if err := json.Unmarshal(actual, &anyVal); err != nil {
		t.Fatalf("unmarshal actual: %v", err)
	}
	normalised, err := json.MarshalIndent(anyVal, "", "  ")
	if err != nil {
		t.Fatalf("marshal actual: %v", err)
	}
	normalised = append(normalised, '\n')

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(goldenPath), err)
		}
		if err := os.WriteFile(goldenPath, normalised, 0644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v — run with -update to create it", goldenPath, err)
	}

	var wantVal any
	if err := json.Unmarshal(expected, &wantVal); err != nil {
		t.Fatalf("unmarshal golden %s: %v", goldenPath, err)
	}

	if !reflect.DeepEqual(anyVal, wantVal) {
		t.Errorf("golden mismatch: %s\ngot:\n%s\nwant:\n%s", goldenPath, normalised, expected)
	}
}

func td(name string) string {
	return inputBase + name + ".json"
}

// TestNormalizeNetboxChassis: F5-R5900 with serial → Chassis + NetworkDevice + RESIDES_IN.
func TestNormalizeNetboxChassis(t *testing.T) {
	records := normalize(t, "netbox.Device", inputBase+"netbox_device_chassis.json")

	byType := make(map[string]ingest.IngestRecord)
	for _, r := range records {
		byType[r.Schema.LogicalArtifactType] = r
	}

	if len(records) != 3 {
		t.Fatalf("want 3 records, got %d: %v", len(records), keys(byType))
	}

	chassis, ok := byType["canonical_chassis"]
	if !ok {
		t.Fatal("missing canonical_chassis record")
	}
	assertGolden(t, chassis.Payload, td("canonical_chassis"))

	nd, ok := byType["canonical_network_device"]
	if !ok {
		t.Fatal("missing canonical_network_device record")
	}
	assertGolden(t, nd.Payload, td("canonical_network_device"))

	edge, ok := byType["canonical_relation"]
	if !ok {
		t.Fatal("missing canonical_relation record")
	}
	assertGolden(t, edge.Payload, td("canonical_relation_resides_in"))
}

// TestNormalizeNetboxLBGuest: F5-TENANT → LBGuest + HOSTS edge.
func TestNormalizeNetboxLBGuest(t *testing.T) {
	records := normalize(t, "netbox.Device", inputBase+"netbox_device_lb_guest.json")

	byType := make(map[string]ingest.IngestRecord)
	for _, r := range records {
		byType[r.Schema.LogicalArtifactType] = r
	}

	if len(records) != 2 {
		t.Fatalf("want 2 records, got %d: %v", len(records), keys(byType))
	}

	guest, ok := byType["canonical_lb_guest"]
	if !ok {
		t.Fatal("missing canonical_lb_guest record")
	}
	assertGolden(t, guest.Payload, td("canonical_lb_guest"))

	edge, ok := byType["canonical_relation"]
	if !ok {
		t.Fatal("missing canonical_relation record")
	}
	assertGolden(t, edge.Payload, td("canonical_relation_hosts"))
}

// TestNormalizeOctobusLog: OTLP-wrapped syslog → canonical LogRecord.
func TestNormalizeOctobusLog(t *testing.T) {
	records := normalize(t, "generic.log", inputBase+"generic_log.json")

	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	assertGolden(t, records[0].Payload, td("canonical_octobus_logs"))
}

func TestNormalizeUnknownSchema(t *testing.T) {
	stub := &sourcemapping.Stub{}
	_, err := stub.Normalize(context.Background(), ingest.SourceMapping{}, ingest.IngestRecord{
		Schema:  ingest.SchemaKey{LogicalArtifactType: "unknown.type", Version: "0.1.0"},
		Payload: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("expected error for unknown schema type, got nil")
	}
}

func keys(m map[string]ingest.IngestRecord) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
