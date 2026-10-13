// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

// IngestRecord is a single envelope passed to the pipeline.
type IngestRecord struct {
	LogicalArtifactID string
	SchemaVersion     string
	Payload           []byte
	Partition         string
	Watermark         map[string]any
	ContentType       *string
}

// IngestResult summarises the outcome of a batch ingest call.
type IngestResult struct {
	Accepted int
	Rejected int
}
