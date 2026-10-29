// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import "github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"

// lastAcceptedWatermarks returns the last accepted watermark per partition by
// scanning batch in reverse. numPartitions is the number of distinct partitions
// in the batch; when > 0 the scan stops early once all partitions are found.
// Pass 0 when the count is unknown — the full batch is scanned.
func lastAcceptedWatermarks(batch []ingest.IngestRecord, outcomes []bool, numPartitions int) map[string]map[string]any {
	result := make(map[string]map[string]any)
	for i := len(batch) - 1; i >= 0; i-- {
		if numPartitions > 0 && len(result) == numPartitions {
			break
		}
		if i >= len(outcomes) || !outcomes[i] || batch[i].Watermark == nil {
			continue
		}
		if _, seen := result[batch[i].Partition]; !seen {
			result[batch[i].Partition] = batch[i].Watermark
		}
	}
	return result
}
