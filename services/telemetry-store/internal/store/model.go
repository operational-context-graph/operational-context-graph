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
	Timestamp          time.Time  `json:"timestamp"`
	IRI                string     `json:"iri"`
	ObservedTimestamp  *time.Time `json:"observed_timestamp,omitempty"`
	HostName           *string    `json:"host_name,omitempty"`
	Severity           *string    `json:"severity,omitempty"`
	Body               *string    `json:"body,omitempty"`
	ResourceAttributes *string    `json:"resource_attributes,omitempty"`
	Attributes         *string    `json:"attributes,omitempty"`
}

// Metric is one row from ocg.metrics.
type Metric struct {
	Timestamp          time.Time  `json:"timestamp"`
	IRI                string     `json:"iri"`
	ObservedTimestamp  *time.Time `json:"observed_timestamp,omitempty"`
	HostName           *string    `json:"host_name,omitempty"`
	MetricName         *string    `json:"metric_name,omitempty"`
	Value              *float64   `json:"value,omitempty"`
	ResourceAttributes *string    `json:"resource_attributes,omitempty"`
	Attributes         *string    `json:"attributes,omitempty"`
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

// clampLimit enforces a valid limit: zero/negative → 1000, above 10 000 → 1000.
func clampLimit(n int) int {
	if n <= 0 || n > 10000 {
		return 1000
	}
	return n
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
