//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/netbox"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/publisher"
)

const (
	mockBaseURL     = "http://localhost:8081"
	schemaVersion   = "0.1.0"
)

// coreEndpoints are small endpoints from mockreplay (< 10k records each).
// Fast enough to complete within a 60s timeout.
var coreEndpoints = []struct {
	path    string
	lat     string
	wantMin int
}{
	{"dcim/sites/",              "netbox.Site",        125},
	{"dcim/regions/",            "netbox.Region",       35},  // not in mock — expects 404 skip
	{"dcim/locations/",          "netbox.Location",   2659},
	{"dcim/device-types/",       "netbox.DeviceType",  412},
	{"dcim/platforms/",          "netbox.Platform",     53},
	{"virtualization/clusters/", "netbox.Cluster",    4899},
}

// slowEndpoints require SLOW=1 — large record counts.
var slowEndpoints = []struct {
	path    string
	lat     string
	wantMin int
}{
	{"dcim/devices/",      "netbox.Device",    65775},
	{"dcim/interfaces/",   "netbox.Interface", 756880},
	{"ipam/ip-addresses/", "netbox.IPAddress", 104705},
	{"dcim/racks/",        "netbox.Rack",        7730},
}

// TestIntegration_CoreEndpoints validates the full pipeline against the 8 fast
// mockreplay endpoints. Runs in < 2 minutes.
//
// Prerequisites:
//
//	cd mockreplay && uv run mockreplay serve netbox --db demo.db --port 8081
//
// Run:
//
//	go test -tags=integration -v ./integration/ -run TestIntegration_CoreEndpoints -timeout 120s
func TestIntegration_CoreEndpoints(t *testing.T) {
	nb := netbox.NewHTTPClient(mockBaseURL, "", 1000)
	stub := &publisher.StubPublisher{}
	ctx := context.Background()

	for _, ep := range coreEndpoints {
		ep := ep
		t.Run(ep.path, func(t *testing.T) {
			runEndpoint(t, ctx, nb, stub, ep.path, ep.lat, ep.wantMin)
		})
	}

	t.Logf("total messages published across core endpoints: %d", len(stub.Messages()))
}

// TestIntegration_SlowEndpoints validates large endpoints (756k+ records).
// Requires SLOW=1 env var and -timeout 600s.
//
// Run:
//
//	SLOW=1 go test -tags=integration -v ./integration/ -run TestIntegration_SlowEndpoints -timeout 600s
func TestIntegration_SlowEndpoints(t *testing.T) {
	if os.Getenv("SLOW") != "1" {
		t.Skip("set SLOW=1 to run large-endpoint tests")
	}

	nb := netbox.NewHTTPClient(mockBaseURL, "", 1000)
	stub := &publisher.StubPublisher{}
	ctx := context.Background()

	for _, ep := range slowEndpoints {
		ep := ep
		t.Run(ep.path, func(t *testing.T) {
			runEndpoint(t, ctx, nb, stub, ep.path, ep.lat, ep.wantMin)
		})
	}
}

func runEndpoint(t *testing.T, ctx context.Context, nb netbox.Client, stub *publisher.StubPublisher, path, lat string, wantMin int) {
	t.Helper()

	partition := map[string]any{"endpoint": path}
	require.NoError(t, stub.Open(ctx, partition))

	records, err := nb.Paginate(ctx, path)
	if err != nil {
		// 404 means the mock does not serve this endpoint — skip gracefully.
		if strings.Contains(err.Error(), "404") {
			t.Skipf("endpoint %s not served by mock (404)", path)
		}
		require.NoError(t, err, "paginate %s", path)
	}

	var msgs []envelope.Message
	for _, raw := range records {
		msg, err := envelope.Build(raw, path, lat, schemaVersion)
		require.NoError(t, err)
		msgs = append(msgs, msg)
	}

	require.NoError(t, stub.Publish(ctx, msgs))
	require.NoError(t, stub.Close(ctx))

	assert.GreaterOrEqual(t, len(msgs), wantMin,
		"endpoint %s: got %d records, want at least %d", path, len(msgs), wantMin)

	// Spot-check first message shape.
	if len(msgs) > 0 {
		first := msgs[0]
		assert.Equal(t, lat, first.LogicalArtifactId)
		assert.Equal(t, schemaVersion, first.SchemaVersion)
		assert.Equal(t, "application/json", first.ContentType)
		assert.Equal(t, map[string]any{"endpoint": path}, first.Partition)
		assert.NotEmpty(t, first.Payload)
		assert.Contains(t, first.Watermark, "last_id")
	}
}
