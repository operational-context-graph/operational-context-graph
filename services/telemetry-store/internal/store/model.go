// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// Log is one row from ocg.logs.
type Log struct {
	Timestamp          time.Time `json:"timestamp"`
	IRI                string    `json:"iri"`
	ObservedTimestamp  *time.Time `json:"observed_timestamp,omitempty"`
	HostName           *string   `json:"host_name,omitempty"`
	Severity           *string   `json:"severity,omitempty"`
	Body               *string   `json:"body,omitempty"`
	ResourceAttributes *string   `json:"resource_attributes,omitempty"`
	Attributes         *string   `json:"attributes,omitempty"`
}

// Metric is one row from ocg.metrics.
type Metric struct {
	Timestamp          time.Time `json:"timestamp"`
	IRI                string    `json:"iri"`
	ObservedTimestamp  *time.Time `json:"observed_timestamp,omitempty"`
	HostName           *string   `json:"host_name,omitempty"`
	MetricName         *string   `json:"metric_name,omitempty"`
	Value              *float64  `json:"value,omitempty"`
	ResourceAttributes *string   `json:"resource_attributes,omitempty"`
	Attributes         *string   `json:"attributes,omitempty"`
}

// Trace is one row from ocg.traces.
type Trace struct {
	Timestamp          time.Time `json:"timestamp"`
	IRI                string    `json:"iri"`
	ObservedTimestamp  *time.Time `json:"observed_timestamp,omitempty"`
	HostName           *string   `json:"host_name,omitempty"`
	TraceID            *string   `json:"trace_id,omitempty"`
	SpanID             *string   `json:"span_id,omitempty"`
	ParentSpanID       *string   `json:"parent_span_id,omitempty"`
	SpanName           *string   `json:"span_name,omitempty"`
	DurationNanos      *int64    `json:"duration_nanos,omitempty"`
	StatusCode         *string   `json:"status_code,omitempty"`
	ResourceAttributes *string   `json:"resource_attributes,omitempty"`
	Attributes         *string   `json:"attributes,omitempty"`
}

// LogQuery parameters for GET /v1/telemetry/logs.
type LogQuery struct {
	From     time.Time
	To       time.Time
	HostName string
	Severity string
	Limit    int
	AfterTS  *time.Time
	AfterIRI *string
}

// MetricQuery parameters for GET /v1/telemetry/metrics.
type MetricQuery struct {
	From       time.Time
	To         time.Time
	HostName   string
	MetricName string
	Limit      int
	AfterTS    *time.Time
	AfterIRI   *string
}

// TraceQuery parameters for GET /v1/telemetry/traces/:traceID.
type TraceQuery struct {
	From     time.Time
	To       time.Time
	TraceID  string
	Limit    int
	AfterTS  *time.Time
	AfterIRI *string
}

// Cursor encodes the keyset pagination position as (timestamp, iri) per SDD §5.2.
type Cursor struct {
	TS  time.Time `json:"t"`
	IRI string    `json:"i"`
}

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	return c, json.Unmarshal(b, &c)
}
