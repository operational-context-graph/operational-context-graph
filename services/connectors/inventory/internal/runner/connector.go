package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/netbox"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/publisher"
)

type Result struct {
	Published int
	Skipped   int
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
	watermarks := publisher.NewWatermarkReader(grpcPublisher.Conn(), cfg.GRPC.ConnectorID)
	return New(grpcPublisher, netboxClient, watermarks, cfg), nil
}

func (connector *Connector) Run(ctx context.Context) (Result, error) {
	totals := Result{}
	for _, endpoint := range connector.endpoints() {
		if ctx.Err() != nil {
			break
		}
		connector.logWatermark(ctx, endpoint)
		result, err := connector.ProcessEndpoint(ctx, endpoint)
		if err != nil {
			return totals, fmt.Errorf("%s: %w", endpoint, err)
		}
		totals.Published += result.Published
		totals.Skipped += result.Skipped
	}
	return totals, nil
}

// logWatermark reports the last committed backfill watermark for an endpoint.
// It is observe-only: a failure is logged and the run continues, because this
// does not affect what the connector ingests.
func (connector *Connector) logWatermark(ctx context.Context, endpoint string) {
	partition := map[string]any{"endpoint": endpoint}
	watermark, err := connector.watermarks.GetWatermark(ctx, partition)
	switch {
	case err != nil:
		slog.Warn("failed to read backfill watermark", "endpoint", endpoint, "err", err)
	case watermark == nil:
		slog.Info("no backfill watermark recorded", "endpoint", endpoint)
	default:
		slog.Info("backfill watermark", "endpoint", endpoint, "watermark", watermark)
	}
}

func (connector *Connector) endpoints() []string {
	return connector.cfg.Endpoints
}

func (connector *Connector) ProcessEndpoint(ctx context.Context, endpoint string) (Result, error) {
	records, err := connector.netbox.Paginate(ctx, endpoint)
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

	if err = connector.publisher.Publish(ctx, messages); err != nil {
		return Result{}, err
	}
	return Result{Published: len(messages)}, nil
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
