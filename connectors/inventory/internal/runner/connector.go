package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/config"
	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/envelope"
	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/netbox"
	"github.com/operational-context-graph/operational-context-graph/connectors/inventory/internal/publisher"
)

type Result struct {
	Published int
	Skipped   int
}

type Connector struct {
	publisher    publisher.Publisher
	netbox       netbox.Client
	cfg          *config.Config
}

func New(pub publisher.Publisher, nb netbox.Client, cfg *config.Config) *Connector {
	return &Connector{publisher: pub, netbox: nb, cfg: cfg}
}

func NewFromConfig(cfg *config.Config) (*Connector, error) {
	nb := netbox.NewHTTPClient(cfg.NetBox.BaseURL, cfg.NetBox.Token, cfg.NetBox.PageSize)
	pub, err := publisher.NewGRPCPublisher(cfg.GRPC.Endpoint, cfg.GRPC.CertFile, cfg.GRPC.KeyFile, cfg.GRPC.ConnectorID)
	if err != nil {
		return nil, err
	}
	return New(pub, nb, cfg), nil
}

func (c *Connector) Run(ctx context.Context) (Result, error) {
	totals := Result{}
	for _, endpoint := range c.endpoints() {
		if ctx.Err() != nil {
			break
		}
		result, err := c.ProcessEndpoint(ctx, endpoint)
		if err != nil {
			return totals, fmt.Errorf("%s: %w", endpoint, err)
		}
		totals.Published += result.Published
		totals.Skipped += result.Skipped
	}
	return totals, nil
}

func (c *Connector) endpoints() []string {
	if len(c.cfg.Connector.Endpoints) > 0 {
		return c.cfg.Connector.Endpoints
	}
	return netbox.DefaultEndpoints
}

func (c *Connector) ProcessEndpoint(ctx context.Context, endpoint string) (Result, error) {
	records, err := c.netbox.Paginate(ctx, endpoint)
	if err != nil {
		return Result{}, err
	}
	if len(records) == 0 {
		slog.Debug("endpoint empty, skipping", "endpoint", endpoint)
		return Result{Skipped: 1}, nil
	}

	messages := c.buildEnvelopes(endpoint, records)

	if err = c.publisher.Publish(ctx, messages); err != nil {
		return Result{}, err
	}
	return Result{Published: len(messages)}, nil
}

func (c *Connector) buildEnvelopes(endpoint string, records []json.RawMessage) []envelope.Message {
	artifactType := c.cfg.Connector.LogicalArtifactIds[endpoint]
	if artifactType == "" {
		artifactType = "netbox.Unknown"
	}
	messages := make([]envelope.Message, 0, len(records))
	for _, raw := range records {
		msg, err := envelope.Build(raw, endpoint, artifactType, c.cfg.Connector.SchemaVersion)
		if err == nil {
			messages = append(messages, msg)
		} else {
			slog.Warn("failed to build envelope", "endpoint", endpoint, "err", err)
		}
	}
	return messages
}
