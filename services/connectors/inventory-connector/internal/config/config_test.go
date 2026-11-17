package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_EnvOnly(t *testing.T) {
	t.Setenv("CONNECTOR_NETBOX__BASE_URL", "http://netbox.example.com")
	t.Setenv("CONNECTOR_NETBOX__PAGE_SIZE", "500")
	t.Setenv("CONNECTOR_GRPC__ENDPOINT", "data-ingestion:9090")
	t.Setenv("CONNECTOR_GRPC__CONNECTOR_ID", "test-connector")
	t.Setenv("CONNECTOR_SCHEMA_VERSION", "0.1.0")
	t.Setenv("CONNECTOR_ENDPOINTS", "dcim/devices/,dcim/sites/")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "http://netbox.example.com", cfg.NetBox.BaseURL)
	assert.Equal(t, 500, cfg.NetBox.PageSize)
	assert.Equal(t, "data-ingestion:9090", cfg.GRPC.Endpoint)
	assert.Equal(t, "test-connector", cfg.GRPC.ConnectorID)
	assert.Equal(t, "0.1.0", cfg.SchemaVersion)
	assert.Equal(t, []string{"dcim/devices/", "dcim/sites/"}, cfg.Endpoints)
}

func TestLoad_ArtifactTypesJSON(t *testing.T) {
	t.Setenv("CONNECTOR_NETBOX__BASE_URL", "http://netbox.example.com")
	t.Setenv("CONNECTOR_NETBOX__PAGE_SIZE", "500")
	t.Setenv("CONNECTOR_GRPC__ENDPOINT", "data-ingestion:9090")
	t.Setenv("CONNECTOR_GRPC__CONNECTOR_ID", "test-connector")
	t.Setenv("CONNECTOR_SCHEMA_VERSION", "0.1.0")
	t.Setenv("CONNECTOR_ENDPOINTS", "dcim/devices/,ipam/prefixes/")
	t.Setenv("CONNECTOR_ARTIFACT_TYPES", `{"dcim/devices/":"netbox.Device","ipam/prefixes/":"netbox.Prefix"}`)

	cfg, err := Load()

	require.NoError(t, err)
	assert.Equal(t, "netbox.Device", cfg.ArtifactTypes["dcim/devices/"])
	assert.Equal(t, "netbox.Prefix", cfg.ArtifactTypes["ipam/prefixes/"])
}

func TestLoad_ArtifactTypesInvalidJSON(t *testing.T) {
	t.Setenv("CONNECTOR_NETBOX__BASE_URL", "http://netbox.example.com")
	t.Setenv("CONNECTOR_NETBOX__PAGE_SIZE", "500")
	t.Setenv("CONNECTOR_GRPC__ENDPOINT", "data-ingestion:9090")
	t.Setenv("CONNECTOR_GRPC__CONNECTOR_ID", "test-connector")
	t.Setenv("CONNECTOR_SCHEMA_VERSION", "0.1.0")
	t.Setenv("CONNECTOR_ENDPOINTS", "dcim/devices/")
	t.Setenv("CONNECTOR_ARTIFACT_TYPES", `not-json`)

	_, err := Load()

	require.ErrorContains(t, err, "CONNECTOR_ARTIFACT_TYPES")
}
