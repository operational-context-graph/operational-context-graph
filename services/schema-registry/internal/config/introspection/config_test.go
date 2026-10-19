// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package introspection

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validConfigTemplate = `
server:
  port: 8081
database:
  url: postgresql://registry:password@db.example.test:5432/registry?sslmode=verify-full
cursor:
  active_key:
    id: current
    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
    deadline: %s
admission:
  max_in_flight: 7
`

func TestLoadPrecedenceAndRedaction(t *testing.T) {
	path := writeConfig(t, validConfig(t))
	t.Setenv("OCG_SCHEMA_INTROSPECTION_ADMISSION__MAX_IN_FLIGHT", "3")
	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Admission.MaxInFlight)
	assert.NotContains(t, values(cfg.Redacted()), "password")
	assert.NotContains(t, values(cfg.Redacted()), cfg.Cursor.ActiveKey.Secret)
}

func TestLoadRequiresAdmissionLimit(t *testing.T) {
	contents := strings.Replace(validConfig(t), "admission:\n  max_in_flight: 7\n", "", 1)
	_, err := LoadFromFile(writeConfig(t, contents))
	require.Error(t, err)
}

func TestLoadAllowsSingleAPISlotWithReservedReadinessCapacity(t *testing.T) {
	contents := strings.Replace(validConfig(t), "max_in_flight: 7", "max_in_flight: 1", 1)
	cfg, err := LoadFromFile(writeConfig(t, contents))
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Admission.MaxInFlight)
	assert.Greater(t, cfg.Database.PoolMaxConnections-cfg.Database.ReservedConnections, int32(0))
}

func TestLoadRejectsInvalidBoundaries(t *testing.T) {
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
		"weak key":                   "cursor:\n  active_key:\n    secret: YQ==\n",
		"duplicate identifiers":      "cursor:\n  previous_keys:\n  - id: current\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: 2099-01-02T00:00:00Z\n",
		"passed deadline":            "cursor:\n  active_key:\n    deadline: 2000-01-01T00:00:00Z\n",
		"excessive previous keys":    "cursor:\n  previous_keys:\n  - id: one\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: 2099-01-02T00:00:00Z\n  - id: two\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: 2099-01-02T00:00:00Z\n  - id: three\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: 2099-01-02T00:00:00Z\n  - id: four\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: 2099-01-02T00:00:00Z\n",
	}
	for name, override := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromFile(writeConfig(t, validConfig(t)+override))
			require.Error(t, err)
		})
	}
}

func TestLoadRejectsCursorKeySecurityWindows(t *testing.T) {
	previousDeadline := time.Now().Add(18 * time.Hour).UTC().Format(time.RFC3339)
	tests := map[string]string{
		"active lifetime too long": fmt.Sprintf(validConfigTemplate, time.Now().Add(maxCursorKeyLifetime+time.Hour).UTC().Format(time.RFC3339)),
		"previous outlives active": validConfig(t) + fmt.Sprintf("cursor:\n  previous_keys:\n  - id: previous\n    secret: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    deadline: %s\n", previousDeadline),
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromFile(writeConfig(t, contents))
			require.Error(t, err)
		})
	}
}

func validConfig(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(validConfigTemplate, time.Now().Add(12*time.Hour).UTC().Format(time.RFC3339))
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func values(values map[string]any) string {
	return strings.TrimSpace(strings.Join([]string{values["database_host"].(string), values["cursor_active_key_id"].(string)}, " "))
}
