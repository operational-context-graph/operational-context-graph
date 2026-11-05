package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

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
		result, err := connector.ProcessEndpoint(ctx, endpoint, connector.deltaParams(ctx, endpoint))
		if err != nil {
			return totals, fmt.Errorf("%s: %w", endpoint, err)
		}
		totals.Published += result.Published
		totals.Skipped += result.Skipped
	}
	return totals, nil
}

// deltaParams returns last_updated__gt + ordering filter when a watermark exists,
// nil for a full scan (bootstrap or on error).
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
	params.Set("last_updated__gt", lastUpdated)
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
