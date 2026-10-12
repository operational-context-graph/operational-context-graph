// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package v1

// LogResponse is one log row returned by GET /v1/telemetry/logs.
type LogResponse struct {
	Timestamp  string  `json:"timestamp"`
	RecordID   string  `json:"record_id"`
	Host       *string `json:"host,omitempty"`
	Severity   *string `json:"severity,omitempty"`
	Body       *string `json:"body,omitempty"`
	Attributes *string `json:"attributes,omitempty"`
}

// MetricResponse is one metric row returned by GET /v1/telemetry/metrics.
type MetricResponse struct {
	Timestamp  string   `json:"timestamp"`
	RecordID   string   `json:"record_id"`
	Host       *string  `json:"host,omitempty"`
	MetricName *string  `json:"metric_name,omitempty"`
	Value      *float64 `json:"value,omitempty"`
	Attributes *string  `json:"attributes,omitempty"`
}

// TraceResponse is one trace span returned by GET /v1/telemetry/traces/:traceID.
type TraceResponse struct {
	Timestamp     string  `json:"timestamp"`
	RecordID      string  `json:"record_id"`
	Host          *string `json:"host,omitempty"`
	TraceID       *string `json:"trace_id,omitempty"`
	SpanID        *string `json:"span_id,omitempty"`
	ParentSpanID  *string `json:"parent_span_id,omitempty"`
	SpanName      *string `json:"span_name,omitempty"`
	DurationNanos *int64  `json:"duration_nanos,omitempty"`
	StatusCode    *string `json:"status_code,omitempty"`
	Attributes    *string `json:"attributes,omitempty"`
}

// LogListResponse is the body for GET /v1/telemetry/logs.
type LogListResponse struct {
	Items      []LogResponse `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// MetricListResponse is the body for GET /v1/telemetry/metrics.
type MetricListResponse struct {
	Items      []MetricResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// TraceListResponse is the body for GET /v1/telemetry/traces/:traceID.
type TraceListResponse struct {
	Items []TraceResponse `json:"items"`
}
