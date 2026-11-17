package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/backfill"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/envelope"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/netbox"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory-connector/internal/publisher"
)

type Result struct {
	Published int
	Skipped   int
	Rejected  int
}

// watermarkReader reads the last committed backfill watermark for a partition.
// It is satisfied by publisher.WatermarkReader and faked in tests.
type watermarkReader interface {
	GetWatermark(ctx context.Context, partition map[string]any) (map[string]any, error)
}

type Connector struct {
	publisher  publisher.Publisher
	netbox     netbox.Client
	watermarks watermarkReader
	cfg        *config.Config
}

func New(pub publisher.Publisher, nb netbox.Client, watermarks watermarkReader, cfg *config.Config) *Connector {
	return &Connector{publisher: pub, netbox: nb, watermarks: watermarks, cfg: cfg}
}

func NewFromConfig(cfg *config.Config) (*Connector, error) {
	netboxClient := netbox.NewHTTPClient(cfg.NetBox.BaseURL, cfg.NetBox.Token, cfg.NetBox.PageSize)
	grpcPublisher, err := publisher.NewGRPCPublisher(cfg.GRPC.Endpoint, cfg.GRPC.CertFile, cfg.GRPC.KeyFile, cfg.GRPC.ConnectorID)
	if err != nil {
		return nil, err
	}
	watermarks := backfill.NewBackfillClient(grpcPublisher.Conn(), cfg.GRPC.ConnectorID)
	return New(grpcPublisher, netboxClient, watermarks, cfg), nil
}

func (connector *Connector) Run(ctx context.Context) (Result, error) {
	totals := Result{}
	for _, endpoint := range connector.endpoints() {
		if ctx.Err() != nil {
			break
		}
		result, err := connector.ProcessEndpoint(ctx, endpoint, connector.deltaParams(ctx, endpoint))
		if err != nil {
			return totals, fmt.Errorf("%s: %w", endpoint, err)
		}
		totals.Published += result.Published
		totals.Skipped += result.Skipped
		totals.Rejected += result.Rejected
	}
	return totals, nil
}

// deltaParams returns last_updated__gte + ordering filter when a watermark exists,
// nil for a full scan (bootstrap or on error). The filter is __gte, not __gt, so a
// record sharing the watermark's exact timestamp is never skipped; re-delivering it is
// safe because the Inventory Store upserts by natural key and a matching
// last_updated_at_source produces no new row.
func (connector *Connector) deltaParams(ctx context.Context, endpoint string) url.Values {
	watermark, err := connector.watermarks.GetWatermark(ctx, map[string]any{"endpoint": endpoint})
	if err != nil {
		slog.Warn("failed to read watermark, running full scan", "endpoint", endpoint, "err", err)
		return nil
	}
	lastUpdated, _ := watermark["last_updated"].(string)
	if lastUpdated == "" {
		slog.Info("no watermark, running full scan", "endpoint", endpoint)
		return nil
	}
	slog.Info("delta fetch", "endpoint", endpoint, "last_updated", lastUpdated)
	params := url.Values{}
	params.Set("last_updated__gte", lastUpdated)
	params.Set("ordering", "last_updated")
	return params
}

func (connector *Connector) endpoints() []string {
	return connector.cfg.Endpoints
}

func (connector *Connector) ProcessEndpoint(ctx context.Context, endpoint string, params url.Values) (Result, error) {
	records, err := connector.netbox.Paginate(ctx, endpoint, params)
	if errors.Is(err, netbox.ErrNotFound) {
		slog.Debug("endpoint not found, skipping", "endpoint", endpoint)
		return Result{Skipped: 1}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if len(records) == 0 {
		slog.Debug("endpoint empty, skipping", "endpoint", endpoint)
		return Result{Skipped: 1}, nil
	}

	messages := connector.buildEnvelopes(endpoint, records)

	pub, err := connector.publisher.Publish(ctx, messages)
	if err != nil {
		return Result{}, err
	}
	// Data ingestion rejects records whose type has no schema, normalizer, or
	// target store. Only netbox.Device passes all three today, so every other
	// endpoint is fully rejected. Count accepted records as published and log the
	// rejected count loudly so a run of an unsupported type does not look like success.
	if pub.Rejected > 0 {
		slog.Warn("records rejected by data ingestion",
			"endpoint", endpoint, "rejected", pub.Rejected, "sent", len(messages))
	}
	accepted := len(messages) - pub.Rejected
	if accepted < 0 {
		accepted = 0
	}
	return Result{Published: accepted, Rejected: pub.Rejected}, nil
}

func (connector *Connector) buildEnvelopes(endpoint string, records []json.RawMessage) []envelope.Message {
	artifactType := connector.cfg.ArtifactTypes[endpoint]
	if artifactType == "" {
		artifactType = netbox.DefaultArtifactTypes[endpoint]
	}
	if artifactType == "" {
		artifactType = "netbox.Unknown"
	}
	messages := make([]envelope.Message, 0, len(records))
	for _, raw := range records {
		msg, err := envelope.Build(raw, endpoint, artifactType, connector.cfg.SchemaVersion)
		if err == nil {
			messages = append(messages, msg)
		} else {
			slog.Warn("failed to build envelope", "endpoint", endpoint, "err", err)
		}
	}
	return messages
}
