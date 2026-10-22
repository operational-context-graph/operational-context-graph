// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package sourcemapping_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/sourcemapping"
)

const testdataBase = "../schemaregistry/testdata/"

func normalize(t *testing.T, schemaType, file string) []ingest.IngestRecord {
	t.Helper()
	payload, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	stub := &sourcemapping.Stub{}
	records, err := stub.Normalize(context.Background(), ingest.SourceMapping{}, ingest.IngestRecord{
		Schema:  ingest.SchemaKey{LogicalArtifactType: schemaType, Version: "1.0.0"},
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("Normalize(%s): %v", file, err)
	}
	return records
}

func field(t *testing.T, payload []byte, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m[key]
}

func TestNormalizeNetboxChassis(t *testing.T) {
	records := normalize(t, "netbox.device", testdataBase+"netbox_device_chassis.json")

	types := make(map[string]ingest.IngestRecord)
	for _, r := range records {
		types[r.Schema.LogicalArtifactType] = r
	}

	// F5-R5900 with serial → Chassis + NetworkDevice + RESIDES_IN
	for _, want := range []string{"canonical_chassis", "canonical_network_device", "canonical_relation"} {
		if _, ok := types[want]; !ok {
			t.Errorf("missing output record of type %q; got types: %v", want, keys(types))
		}
	}
	if len(records) != 3 {
		t.Errorf("want 3 records, got %d", len(records))
	}

	chassis := types["canonical_chassis"]
	if got := field(t, chassis.Payload, "artifact_type"); got != "Chassis" {
		t.Errorf("artifact_type = %v, want Chassis", got)
	}
	if got := field(t, chassis.Payload, "source_system"); got != "netbox" {
		t.Errorf("source_system = %v, want netbox", got)
	}
	if got, _ := field(t, chassis.Payload, "iri").(string); got == "" {
		t.Error("chassis IRI is empty")
	}
}

func TestNormalizeNetboxLBGuest(t *testing.T) {
	records := normalize(t, "netbox.device", testdataBase+"netbox_device_lb_guest.json")

	types := make(map[string]ingest.IngestRecord)
	for _, r := range records {
		types[r.Schema.LogicalArtifactType] = r
	}

	// F5-TENANT → LBGuest + HOSTS edge
	for _, want := range []string{"canonical_lb_guest", "canonical_relation"} {
		if _, ok := types[want]; !ok {
			t.Errorf("missing output record of type %q; got types: %v", want, keys(types))
		}
	}
	if len(records) != 2 {
		t.Errorf("want 2 records, got %d", len(records))
	}

	guest := types["canonical_lb_guest"]
	if got := field(t, guest.Payload, "artifact_type"); got != "LBGuest" {
		t.Errorf("artifact_type = %v, want LBGuest", got)
	}

	edge := types["canonical_relation"]
	if got := field(t, edge.Payload, "artifact_type"); got != "HOSTS" {
		t.Errorf("edge artifact_type = %v, want HOSTS", got)
	}
}

func TestNormalizeOctobusLog(t *testing.T) {
	records := normalize(t, "generic.log", testdataBase+"generic_log.json")

	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	r := records[0]
	if r.Schema.LogicalArtifactType != "canonical_octobus_logs" {
		t.Errorf("schema type = %q, want canonical_octobus_logs", r.Schema.LogicalArtifactType)
	}
	if got := field(t, r.Payload, "artifact_type"); got != "LogRecord" {
		t.Errorf("artifact_type = %v, want LogRecord", got)
	}
	if got := field(t, r.Payload, "severity"); got != "INFO" {
		t.Errorf("severity = %v, want INFO (mapped from 'notice')", got)
	}
	if ts, _ := field(t, r.Payload, "timestamp").(float64); ts == 0 {
		t.Error("timestamp is zero")
	}
}

func TestNormalizeUnknownSchema(t *testing.T) {
	stub := &sourcemapping.Stub{}
	_, err := stub.Normalize(context.Background(), ingest.SourceMapping{}, ingest.IngestRecord{
		Schema:  ingest.SchemaKey{LogicalArtifactType: "unknown.type", Version: "1.0.0"},
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
