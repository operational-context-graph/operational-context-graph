package v1

// LogResponse is one log row returned by GET /v1/telemetry/logs.
type LogResponse struct {
	Timestamp          string  `json:"timestamp"`
	IRI                string  `json:"iri"`
	ObservedTimestamp  *string `json:"observed_timestamp,omitempty"`
	HostName           *string `json:"host_name,omitempty"`
	Severity           *string `json:"severity,omitempty"`
	Body               *string `json:"body,omitempty"`
	ResourceAttributes *string `json:"resource_attributes,omitempty"`
	Attributes         *string `json:"attributes,omitempty"`
}

// MetricResponse is one metric row returned by GET /v1/telemetry/metrics.
type MetricResponse struct {
	Timestamp          string   `json:"timestamp"`
	IRI                string   `json:"iri"`
	ObservedTimestamp  *string  `json:"observed_timestamp,omitempty"`
	HostName           *string  `json:"host_name,omitempty"`
	MetricName         *string  `json:"metric_name,omitempty"`
	Value              *float64 `json:"value,omitempty"`
	ResourceAttributes *string  `json:"resource_attributes,omitempty"`
	Attributes         *string  `json:"attributes,omitempty"`
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

// IngestResponse is the body for POST /v1/telemetry/{logs,metrics}.
type IngestResponse struct {
	LoadedRows   int64  `json:"loaded_rows"`
	FilteredRows int64  `json:"filtered_rows,omitempty"`
	ErrorURL     string `json:"error_url,omitempty"`
}
