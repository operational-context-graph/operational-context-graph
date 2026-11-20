package introspection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:gosec // G101: no real hardcoded credentials, just synthetic example values in the test fixtures
const validConfig = `
server:
  port: 8081
database:
  url: postgresql://registry:password@db.example.test:5432/registry?sslmode=verify-full
admission:
  max_in_flight: 7
`

func TestLoadPrecedenceAndRedaction(t *testing.T) {
	path := writeConfig(t, validConfig)
	t.Setenv("OCG_SCHEMA_INTROSPECTION_ADMISSION__MAX_IN_FLIGHT", "3")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Admission.MaxInFlight)
	assert.NotContains(t, values(cfg.Redacted()), "password")
}

func TestLoadConvertsConfigurationValues(t *testing.T) {
	for _, source := range []string{"yaml", "environment"} {
		t.Run(source, func(t *testing.T) {
			contents := validConfig
			if source == "yaml" {
				contents = strings.Replace(contents, "port: 8081", "port: '9090'\n  read_timeout: 250ms", 1)
				contents = strings.Replace(contents, "database:\n", "database:\n  production: 'true'\n  pool_max_connections: '12'\n  query_timeout: 2s\n", 1)
			} else {
				t.Setenv("OCG_SCHEMA_INTROSPECTION_SERVER__PORT", "9090")
				t.Setenv("OCG_SCHEMA_INTROSPECTION_SERVER__READ_TIMEOUT", "250ms")
				t.Setenv("OCG_SCHEMA_INTROSPECTION_DATABASE__PRODUCTION", "true")
				t.Setenv("OCG_SCHEMA_INTROSPECTION_DATABASE__POOL_MAX_CONNECTIONS", "12")
				t.Setenv("OCG_SCHEMA_INTROSPECTION_DATABASE__QUERY_TIMEOUT", "2s")
			}
			cfg, err := LoadFromFile(writeConfig(t, contents))
			require.NoError(t, err)
			assert.Equal(t, 9090, cfg.Server.Port)
			assert.Equal(t, 250*time.Millisecond, cfg.Server.ReadTimeout)
			assert.True(t, cfg.Database.Production)
			assert.Equal(t, int32(12), cfg.Database.PoolMaxConnections)
			assert.Equal(t, 2*time.Second, cfg.Database.QueryTimeout)
			assert.Equal(t, 60*time.Second, cfg.Server.IdleTimeout)
		})
	}
}

func TestLoadRequiresNoCursorConfiguration(t *testing.T) {
	cfg, err := LoadFromFile(writeConfig(t, validConfig))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Admission.MaxInFlight)
}

func TestLoadRequiresAdmissionLimit(t *testing.T) {
	contents := strings.Replace(validConfig, "admission:\n  max_in_flight: 7\n", "", 1)
	_, err := LoadFromFile(writeConfig(t, contents))
	require.Error(t, err)
}

func TestLoadAllowsSingleAPISlotWithReservedReadinessCapacity(t *testing.T) {
	contents := strings.Replace(validConfig, "max_in_flight: 7", "max_in_flight: 1", 1)
	cfg, err := LoadFromFile(writeConfig(t, contents))
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Admission.MaxInFlight)
	assert.Greater(t, cfg.Database.PoolMaxConnections-cfg.Database.ReservedConnections, int32(0))
}

func TestLoadRejectsInvalidBoundaries(t *testing.T) {
	for _, test := range []struct{ name, key, value, want string }{
		{name: "empty target", key: "DATABASE__URL", want: "Config.Database.URL"},
		{name: "missing runtime login", key: "DATABASE__URL", value: "postgresql://db.example.test/registry?sslmode=verify-full", want: "absolute PostgreSQL URL"},
		{name: "malformed URL", key: "DATABASE__URL", value: "%", want: "absolute PostgreSQL URL"},
		{name: "wrong scheme", key: "DATABASE__URL", value: "https://registry@db.example.test/registry", want: "use postgres or postgresql"},
		{name: "weak TLS", key: "DATABASE__URL", value: "postgresql://registry@db.example.test/registry?sslmode=require", want: "require sslmode=verify-full"},
		{name: "missing TLS", key: "DATABASE__URL", value: "postgresql://registry@db.example.test/registry", want: "require sslmode=verify-full"},
		{name: "port zero", key: "SERVER__PORT", value: "0", want: "Config.Server.Port"},
		{name: "port over range", key: "SERVER__PORT", value: "65536", want: "Config.Server.Port"},
		{name: "zero timeout", key: "SERVER__READ_TIMEOUT", value: "0s", want: "Config.Server.ReadTimeout"},
		{name: "negative timeout", key: "DATABASE__QUERY_TIMEOUT", value: "-1s", want: "Config.Database.QueryTimeout"},
		{name: "pool too small", key: "DATABASE__POOL_MAX_CONNECTIONS", value: "1", want: "Config.Database.PoolMaxConnections"},
		{name: "pool minimum", key: "DATABASE__POOL_MIN_CONNECTIONS", value: "9", want: "pool minimum exceeds maximum"},
		{name: "no reserve", key: "DATABASE__RESERVED_CONNECTIONS", value: "0", want: "Config.Database.ReservedConnections"},
		{name: "exhausted reserve", key: "DATABASE__RESERVED_CONNECTIONS", value: "8", want: "reserved connections exhaust the pool"},
		{name: "admission exceeds capacity", key: "ADMISSION__MAX_IN_FLIGHT", value: "8", want: "exceeds unreserved database capacity"},
		{name: "zero admission", key: "ADMISSION__MAX_IN_FLIGHT", value: "0", want: "Config.Admission.MaxInFlight"},
		{name: "negative retry", key: "ADMISSION__RETRY_INTERVAL", value: "-1s", want: "Config.Admission.RetryInterval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Environment overrides avoid duplicate YAML sections masking validation errors.
			t.Setenv(envPrefix+test.key, test.value)
			cfg, err := LoadFromFile(writeConfig(t, validConfig))
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, cfg)
		})
	}
}

func TestLoadUsesConfiguredFile(t *testing.T) {
	t.Setenv(envFileKey, writeConfig(t, strings.Replace(validConfig, "port: 8081", "port: 9090", 1)))
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.Server.Port)
}

func TestLoadRequiresVerifiedTLSInEveryMode(t *testing.T) {
	for _, production := range []string{"true", "false"} {
		t.Run(production, func(t *testing.T) {
			t.Setenv(envPrefix+"DATABASE__PRODUCTION", production)
			contents := strings.Replace(validConfig, "sslmode=verify-full", "sslmode=disable", 1)
			cfg, err := LoadFromFile(writeConfig(t, contents))
			require.ErrorContains(t, err, "require sslmode=verify-full")
			assert.Nil(t, cfg)
		})
	}
}

func TestLoadReportsSourceAndDecodeFailures(t *testing.T) {
	for _, test := range []struct{ name, contents, key, value, want string }{
		{name: "malformed YAML", contents: "server: [", want: "load configuration file"},
		{name: "invalid integer", contents: validConfig, key: "SERVER__PORT", value: "not-an-integer", want: "decode configuration"},
		{name: "invalid duration", contents: validConfig, key: "DATABASE__QUERY_TIMEOUT", value: "not-a-duration", want: "decode configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.key != "" {
				t.Setenv(envPrefix+test.key, test.value)
			}
			cfg, err := LoadFromFile(writeConfig(t, test.contents))
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, cfg)
		})
	}
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, databaseHost("%"))
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func values(values map[string]any) string {
	return values["database_host"].(string)
}
