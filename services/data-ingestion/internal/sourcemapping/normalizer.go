// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package sourcemapping provides the source mapping execution engine.
package sourcemapping

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

const canonicalVersion = "0.1.0"

// Stub implements ingest.Normalizer with mapping logic hardcoded from the
// netbox_canonical.yaml and octobus_logs.yaml mapping definitions.
// The SourceMapping parameter is ignored.
type Stub struct{}

func (s *Stub) Normalize(_ context.Context, _ ingest.SourceMapping, record ingest.IngestRecord) ([]ingest.IngestRecord, error) {
	switch record.Schema.LogicalArtifactType {
	case "netbox.Device":
		return normalizeNetboxDevice(record)
	case "generic.log":
		return normalizeOctobusLog(record)
	default:
		return nil, fmt.Errorf("%w: no mapping for %q", ingest.ErrNotFound, record.Schema.LogicalArtifactType)
	}
}

// ---------------------------------------------------------------------------
// NetBox device → Chassis / NetworkDevice / LBGuest + edges
// ---------------------------------------------------------------------------

func normalizeNetboxDevice(record ingest.IngestRecord) ([]ingest.IngestRecord, error) {
	var doc map[string]any
	if err := json.Unmarshal(record.Payload, &doc); err != nil {
		return nil, fmt.Errorf("sourcemapping: unmarshal netbox device: %w", err)
	}

	model := getString(doc, "device_type.model")
	serial := getString(doc, "serial")
	idRaw := getPath(doc, "id")
	idStr := fmt.Sprintf("%v", idRaw)
	name := getString(doc, "name")
	created := getString(doc, "created")
	lastUpdated := getString(doc, "last_updated")

	platform := getString(doc, "platform.name")
	ip4raw := getString(doc, "primary_ip4.address")
	ip4 := stripPrefixLength(ip4raw)
	siteSlug := getString(doc, "site.slug")
	status := getString(doc, "status.value")

	var out []ingest.IngestRecord

	// Rule 1 & 4: F5-R5900 with a serial → Chassis + RESIDES_IN edge
	if model == "F5-R5900" && serial != "" {
		chassis := map[string]any{
			"iri":                    "urn:ocg:asset/netbox/chassis-" + serial,
			"artifact_type":          "Chassis",
			"source_system":          "netbox",
			"ingested_at":            time.Now().UTC().Format(time.RFC3339Nano),
			"name":                   name,
			"source_id":              idStr + ":chassis",
			"created_at_source":      created,
			"last_updated_at_source": lastUpdated,
			"extensions": map[string]any{
				"model":            model,
				"serial":           serial,
				"platform":         platform,
				"primary_ip4":      ip4,
				"site_id":          siteSlug,
				"status":           status,
				"netbox_id":        idRaw,
				"network_function": "load_balancer",
			},
		}
		rec, err := toRecord("canonical_chassis", chassis)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)

		edge := map[string]any{
			"artifact_type": "RESIDES_IN",
			"source_system": "netbox",
			"ingested_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"source_iri":    "urn:ocg:asset/netbox/serial-" + serial,
			"target_iri":    "urn:ocg:asset/netbox/chassis-" + serial,
			"source_key":    []string{"netbox", idStr},
			"target_key":    []string{"netbox", idStr + ":chassis"},
			"valid_time":    lastUpdated,
			"extensions":    map[string]any{},
		}
		edgeRec, err := toRecord("canonical_relation", edge)
		if err != nil {
			return nil, err
		}
		out = append(out, edgeRec)
	}

	// Rule 2: F5-R5900, F5-VCMP, F5-i5800 → NetworkDevice
	networkDeviceModels := map[string]bool{"F5-R5900": true, "F5-VCMP": true, "F5-i5800": true}
	if networkDeviceModels[model] {
		iri := "urn:ocg:asset/netbox/serial-" + serial
		if serial == "" {
			iri = "urn:ocg:asset/netbox/netboxid-" + idStr
		}
		nd := map[string]any{
			"iri":                    iri,
			"artifact_type":          "NetworkDevice",
			"source_system":          "netbox",
			"ingested_at":            time.Now().UTC().Format(time.RFC3339Nano),
			"name":                   name,
			"source_id":              idStr,
			"created_at_source":      created,
			"last_updated_at_source": lastUpdated,
			"extensions": map[string]any{
				"model":            model,
				"serial":           serial,
				"platform":         platform,
				"primary_ip4":      ip4,
				"site_id":          siteSlug,
				"status":           status,
				"netbox_id":        idRaw,
				"network_function": "load_balancer",
			},
		}
		rec, err := toRecord("canonical_network_device", nd)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}

	// Rule 3 & 5: F5-TENANT → LBGuest + HOSTS edge
	if model == "F5-TENANT" {
		parentName := getString(doc, "parent_device.name")
		parentIDRaw := getPath(doc, "parent_device.id")
		parentIDStr := fmt.Sprintf("%v", parentIDRaw)
		parentSerial := getString(doc, "parent_device.serial")

		guestIRI := fmt.Sprintf("urn:ocg:dc/%s/%s/%s", siteSlug, parentName, name)

		guest := map[string]any{
			"iri":                    guestIRI,
			"artifact_type":          "LBGuest",
			"source_system":          "netbox",
			"ingested_at":            time.Now().UTC().Format(time.RFC3339Nano),
			"name":                   name,
			"source_id":              idStr,
			"created_at_source":      created,
			"last_updated_at_source": lastUpdated,
			"extensions": map[string]any{
				"model":            model,
				"platform":         platform,
				"primary_ip4":      ip4,
				"site_id":          siteSlug,
				"status":           status,
				"netbox_id":        idRaw,
				"parent_netbox_id": parentIDRaw,
			},
		}
		rec, err := toRecord("canonical_lb_guest", guest)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)

		sourceIRI := "urn:ocg:asset/netbox/netboxid-" + parentIDStr
		if parentIDStr == "" || parentIDStr == "<nil>" {
			sourceIRI = "urn:ocg:asset/netbox/serial-" + parentSerial
		}
		edge := map[string]any{
			"artifact_type": "HOSTS",
			"source_system": "netbox",
			"ingested_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"source_iri":    sourceIRI,
			"target_iri":    guestIRI,
			"source_key":    []string{"netbox", parentIDStr},
			"target_key":    []string{"netbox", idStr},
			"valid_time":    lastUpdated,
			"extensions":    map[string]any{},
		}
		edgeRec, err := toRecord("canonical_relation", edge)
		if err != nil {
			return nil, err
		}
		out = append(out, edgeRec)
	}

	return out, nil
}

// ---------------------------------------------------------------------------
// Octobus log → canonical LogRecord
// ---------------------------------------------------------------------------

func normalizeOctobusLog(record ingest.IngestRecord) ([]ingest.IngestRecord, error) {
	var doc map[string]any
	if err := json.Unmarshal(record.Payload, &doc); err != nil {
		return nil, fmt.Errorf("sourcemapping: unmarshal octobus log: %w", err)
	}

	// Support both OTLP-wrapped format (timeUnixNano + body.stringValue) and
	// flat Octobus syslog format.
	var inner map[string]any
	var tsNanos int64
	var rawTS string

	if tun := getPath(doc, "timeUnixNano"); tun != nil {
		switch v := tun.(type) {
		case string:
			tsNanos, _ = strconv.ParseInt(v, 10, 64)
		case float64:
			tsNanos = int64(v)
		}
		if body := getPath(doc, "body"); body != nil {
			if bodyMap, ok := body.(map[string]any); ok {
				if sv, ok := bodyMap["stringValue"].(string); ok {
					var parsed map[string]any
					if err := json.Unmarshal([]byte(sv), &parsed); err == nil {
						inner = parsed
					}
				}
			}
		}
	}

	if inner == nil {
		inner = doc
		rawTS = getString(doc, "@timestamp")
		tsNanos = toEpochNanos(rawTS)
	} else {
		rawTS = getString(inner, "@timestamp")
	}

	epochSecs := tsNanos / 1_000_000_000

	hostName := getString(inner, "log.syslog.hostname", "fqdn")
	recordID := getString(inner, "sap.ocb.event.hash")
	severity := toSeverityEnum(getString(inner, "syslog_severity"))
	body := getString(inner, "message")

	iri := fmt.Sprintf("urn:ocg:log/octobus/%s:%d:%s", hostName, epochSecs, recordID)

	tags := getPath(inner, "tags")
	if tags == nil {
		tags = []any{}
	}

	payload := map[string]any{
		"iri":                iri,
		"artifact_type":      "LogRecord",
		"source_system":      "octobus",
		"ingested_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"timestamp":          rawTS,
		"observed_timestamp": rawTS,
		"signal_type":        "LogRecord",
		"record_id":          recordID,
		"host_name":          hostName,
		"source_object_id":   hostName,
		"severity":           severity,
		"body":               body,
		"resource_attributes": map[string]any{
			"host.name":    hostName,
			"cluster.name": getString(inner, "sap.ocb.cluster.name"),
			"tenant.id":    getString(inner, "sap.ocb.tenant.id"),
		},
		"attributes": map[string]any{
			"syslog_facility":      getString(inner, "syslog_facility"),
			"syslog_pri":           getPath(inner, "syslog_pri"),
			"syslog_severity_code": getPath(inner, "syslog_severity_code"),
			"tags":                 tags,
		},
		"extensions": map[string]any{
			"event_hash": recordID,
			"fqdn":       getString(inner, "fqdn"),
		},
	}

	rec, err := toRecord("canonical_octobus_logs", payload)
	if err != nil {
		return nil, err
	}
	return []ingest.IngestRecord{rec}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// getPath tries a literal key lookup first (flat dotted keys like
// "log.syslog.hostname"), then falls back to dot-separated nested traversal.
func getPath(doc map[string]any, path string) any {
	if v, ok := doc[path]; ok {
		return v
	}
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		return nil
	}
	child, ok := doc[parts[0]]
	if !ok {
		return nil
	}
	nested, ok := child.(map[string]any)
	if !ok {
		return nil
	}
	return getPath(nested, parts[1])
}

// getString returns the string value at path (with optional fallback paths).
// Returns "" if the value is absent or not a string.
func getString(doc map[string]any, path string, fallbacks ...string) string {
	if v := getPath(doc, path); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	for _, fb := range fallbacks {
		if v := getPath(doc, fb); v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// stripPrefixLength removes a CIDR prefix length: "10.1.2.3/25" → "10.1.2.3".
func stripPrefixLength(s string) string {
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// toEpochNanos parses an RFC 3339 timestamp and returns epoch nanoseconds.
func toEpochNanos(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}

// toEpochSeconds parses an RFC 3339 timestamp and returns epoch seconds.
func toEpochSeconds(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// toSeverityEnum maps a syslog severity string to the canonical severity enum.
func toSeverityEnum(s string) string {
	switch strings.ToLower(s) {
	case "emergency", "alert", "critical":
		return "FATAL"
	case "error":
		return "ERROR"
	case "warning", "warn":
		return "WARN"
	case "notice", "informational", "info":
		return "INFO"
	case "debug":
		return "DEBUG"
	default:
		return "INFO"
	}
}

// toRecord marshals a payload map to an IngestRecord for the given canonical type.
func toRecord(logicalArtifactType string, payload map[string]any) (ingest.IngestRecord, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return ingest.IngestRecord{}, fmt.Errorf("sourcemapping: marshal %s: %w", logicalArtifactType, err)
	}
	return ingest.IngestRecord{
		Schema:      ingest.SchemaKey{LogicalArtifactType: logicalArtifactType, Version: canonicalVersion},
		Payload:     b,
		ContentType: "application/json",
	}, nil
}
