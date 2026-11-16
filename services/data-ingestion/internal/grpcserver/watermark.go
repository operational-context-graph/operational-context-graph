package grpcserver

import (
	"log/slog"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// lastAcceptedWatermarks returns the last accepted watermark per partition by
// scanning batch in reverse. numPartitions is the number of distinct partitions
// in the batch; when > 0 the scan stops early once all partitions are found.
// Pass 0 when the count is unknown — the full batch is scanned.
func lastAcceptedWatermarks(batch []ingest.IngestRecord, accepted []bool, numPartitions int, connectorID string, log *slog.Logger) map[string]map[string]any {
	if len(accepted) != len(batch) {
		log.Error("lastAcceptedWatermarks: accepted length mismatch", "batch", len(batch), "accepted", len(accepted), "connector_id", connectorID)
		return nil
	}
	result := make(map[string]map[string]any)
	for i := len(batch) - 1; i >= 0; i-- {
		if numPartitions > 0 && len(result) == numPartitions {
			break
		}
		if !accepted[i] || batch[i].Watermark == nil {
			continue
		}
		if _, seen := result[batch[i].Partition]; !seen {
			result[batch[i].Partition] = batch[i].Watermark
		}
	}
	return result
}
