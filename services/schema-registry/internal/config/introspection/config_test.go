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
	//nolint:gosec // G101: no real hardcoded credentials, just synthetic example values in the test fixtures
	tests := map[string]string{
		"empty target":               "database:\n  url: ''\n",
		"missing runtime login":      "database:\n  url: postgresql://db.example.test/registry?sslmode=verify-full\n",
		"disabled production TLS":    "database:\n  production: true\n  url: postgresql://registry:password@db.example.test/registry?sslmode=require\n",
		"disabled nonproduction TLS": "database:\n  production: false\n  url: postgresql://registry:password@db.example.test/registry?sslmode=disable\n",
		"port zero":                  "server:\n  port: 0\n",
		"port over range":            "server:\n  port: 65536\n",
		"zero timeout":               "server:\n  read_timeout: 0s\n",
		"negative timeout":           "database:\n  query_timeout: -1s\n",
		"pool too small":             "database:\n  pool_max_connections: 1\n",
		"no reserve":                 "database:\n  reserved_connections: 0\n",
		"admission exceeds capacity": "admission:\n  max_in_flight: 8\n",
		"zero admission":             "admission:\n  max_in_flight: 0\n",
		"negative retry":             "admission:\n  retry_interval: -1s\n",
	}
	for name, override := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromFile(writeConfig(t, validConfig+override))
			require.Error(t, err)
		})
	}
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
