# Partition and watermark shape

Partition: `{"endpoint": "<path>"}` — one stream per NetBox endpoint path (e.g. `dcim/devices/`).
Watermark: `{"last_id": N}` — the NetBox integer `id` field present on every record type.

Integer ID is monotonically increasing and survives restarts. `last_updated` is mutable and not monotonic across bulk imports. Page offsets break on insert/delete between runs.

The connector uses `IngestionService` which does not manage gaps or watermarks — partition and watermark are embedded in each `MessageEnvelope` for downstream use by the OCG pipeline.

## Implementation

`internal/envelope` extracts `last_id` from the raw JSON record when building the message:

```go
func Build(raw json.RawMessage, endpoint, lat, schemaVersion string) (Message, error) {
    var rec struct{ ID int `json:"id"` }
    json.Unmarshal(raw, &rec)   // missing id → zero value, not an error
    return Message{
        Partition: map[string]any{"endpoint": endpoint},
        Watermark: map[string]any{"last_id": rec.ID},
        ...
    }, nil
}
```

See [Run Sequence](../diagrams/overview.md#run-sequence).
