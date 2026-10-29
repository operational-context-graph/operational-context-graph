package sourcemapping_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/sourcemapping"
)

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

// assertSnapshot compares actual JSON payload against a snapshot file.
func assertSnapshot(t *testing.T, actual []byte, snapshotPath string) {
	t.Helper()

	var anyVal any
	if err := json.Unmarshal(actual, &anyVal); err != nil {
		t.Fatalf("unmarshal actual: %v", err)
	}

	// Verify ingested_at is present then remove it before snapshot comparison.
	if m, ok := anyVal.(map[string]any); ok {
		if _, has := m["ingested_at"]; !has {
			t.Error("ingested_at missing from output")
		}
		delete(m, "ingested_at")
	}

	expected, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", snapshotPath, err)
	}

	var wantVal any
	if err := json.Unmarshal(expected, &wantVal); err != nil {
		t.Fatalf("unmarshal snapshot %s: %v", snapshotPath, err)
	}

	if !reflect.DeepEqual(anyVal, wantVal) {
		normalised, _ := json.MarshalIndent(anyVal, "", "  ")
		t.Errorf("snapshot mismatch: %s\ngot:\n%s\nwant:\n%s", snapshotPath, normalised, expected)
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
	assertSnapshot(t, chassis.Payload, td("canonical_chassis"))

	nd, ok := byType["canonical_network_device"]
	if !ok {
		t.Fatal("missing canonical_network_device record")
	}
	assertSnapshot(t, nd.Payload, td("canonical_network_device"))

	edge, ok := byType["canonical_relation"]
	if !ok {
		t.Fatal("missing canonical_relation record")
	}
	assertSnapshot(t, edge.Payload, td("canonical_relation_resides_in"))
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
	assertSnapshot(t, guest.Payload, td("canonical_lb_guest"))

	edge, ok := byType["canonical_relation"]
	if !ok {
		t.Fatal("missing canonical_relation record")
	}
	assertSnapshot(t, edge.Payload, td("canonical_relation_hosts"))
}

// TestNormalizeOctobusLog: OTLP-wrapped syslog → canonical LogRecord.
func TestNormalizeOctobusLog(t *testing.T) {
	records := normalize(t, "generic.log", inputBase+"generic_log.json")

	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	assertSnapshot(t, records[0].Payload, td("canonical_octobus_logs"))
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
