# Structured logging with log/slog

`log/slog` stdlib over `go.uber.org/zap`. The connector is a batch job — high-throughput logging performance is irrelevant. Zero extra dependency, stdlib direction.

## Implementation

Three log levels used across the codebase:

- `slog.Info` — connector start/finish summary, per-endpoint completion with record count
- `slog.Warn` — individual envelope build failures (malformed records are skipped, not fatal)
- `slog.Error` — fatal errors before `os.Exit(1)`: config load failure, publisher init failure, endpoint failure

```go
// cmd/connector/main.go
slog.Info("connector starting", "netbox", cfg.NetBox.BaseURL, "grpc", cfg.GRPC.Endpoint, ...)
slog.Info("connector finished", "published", totals.Published, "endpoints_skipped", totals.Skipped, "duration_ms", ...)

// internal/runner/connector.go
slog.Debug("endpoint empty, skipping", "endpoint", endpoint)
slog.Warn("failed to build envelope", "endpoint", endpoint, "err", err)
```

All log calls use structured key-value pairs — no format strings — so output is machine-parseable in production (JSON handler can be wired at startup without code changes).
