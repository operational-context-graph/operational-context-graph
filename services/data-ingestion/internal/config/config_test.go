// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grpcHostEnv / grpcPortEnv satisfy the required GRPC Host+Port fields.
const grpcHostEnv = "OCG_DI_GRPC__HOST"
const grpcPortEnv = "OCG_DI_GRPC__PORT"
const dbPasswordEnv = "OCG_DI_DATABASE__PASSWORD"

func TestLoadUsesEnvConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 7070\n"), 0o600))
	t.Setenv("OCG_DI_CONFIG_FILE", path)
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 7070, cfg.Server.Port)
}

func TestLoadFromFileReadsOverridesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("logging:\n  level: warn\n"), 0o600))
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.Logging.Level)
}

func TestLoadFromFileMissingFileFails(t *testing.T) {
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
}

func TestDatabaseDSNWithoutMaxConns(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__MAXCONNS", "0")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	dsn := cfg.DatabaseDSN()
	assert.NotContains(t, dsn, "pool_max_conns")
}

// --- existing tests (updated for double-underscore separator) ---

func TestLoadFromFileDefaults(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, 5432, cfg.Database.Port)
	assert.Equal(t, "ocg", cfg.Database.Name)
}

func TestLoadFromFileEnvOverride(t *testing.T) {
	t.Setenv("OCG_DI_SERVER__PORT", "9090")
	t.Setenv("OCG_DI_LOGGING__LEVEL", "debug")
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9091")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "0.0.0.0", cfg.GRPC.Host)
	assert.Equal(t, 9091, cfg.GRPC.Port)
}

func TestLoadFromFileInvalidLogLevel(t *testing.T) {
	t.Setenv("OCG_DI_LOGGING__LEVEL", "verbose")
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileValidDatabaseConfig(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__HOST", "db.internal")
	t.Setenv("OCG_DI_DATABASE__NAME", "ocg_prod")
	t.Setenv("OCG_DI_DATABASE__USER", "ingestor")
	t.Setenv("OCG_DI_DATABASE__PASSWORD", "s3cret")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, "db.internal", cfg.Database.Host)
	assert.Equal(t, "ocg_prod", cfg.Database.Name)
	assert.Equal(t, "ingestor", cfg.Database.User)
	assert.Equal(t, "s3cret", cfg.Database.Password)
}

func TestLoadFromFileMissingDatabaseHostFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__HOST", "")
	t.Setenv(dbPasswordEnv, "testpassword")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

// --- GRPCConfig ---

func TestGRPCDefaultsValid(t *testing.T) {
	t.Setenv(dbPasswordEnv, "testpassword")
	_, err := LoadFromFile("")
	require.NoError(t, err)
}

func TestGRPCPortDefault(t *testing.T) {
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.GRPC.Port)
}

func TestGRPCPortOutOfRange(t *testing.T) {
	t.Setenv(grpcPortEnv, "99999")
	t.Setenv(dbPasswordEnv, "testpassword")
	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileMissingDatabaseNameFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__NAME", "")
	t.Setenv(dbPasswordEnv, "testpassword")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileInvalidDatabasePortFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__PORT", "0")
	t.Setenv(dbPasswordEnv, "testpassword")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestDatabaseDSNContainsCredentialsAndParams(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE__HOST", "db.internal")
	t.Setenv("OCG_DI_DATABASE__NAME", "ocg_prod")
	t.Setenv("OCG_DI_DATABASE__USER", "ingestor")
	t.Setenv("OCG_DI_DATABASE__PASSWORD", "p@ss/word")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	dsn := cfg.DatabaseDSN()
	assert.Contains(t, dsn, "postgres://")
	assert.Contains(t, dsn, "db.internal:5432")
	assert.Contains(t, dsn, "/ocg_prod")
	assert.Contains(t, dsn, "sslmode=disable")
	assert.Contains(t, dsn, "pool_max_conns=10")
	// Password with reserved characters must be URL-escaped, not raw.
	assert.NotContains(t, dsn, "p@ss/word")
	assert.Contains(t, dsn, "p%40ss%2Fword")
}

func TestMissingPasswordFailsValidation(t *testing.T) {
	// OCG_DI_DATABASE__PASSWORD not set — config must be rejected at startup.
	_, err := LoadFromFile("")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "password")
}

func TestGRPCAddrPresent(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	_, err := LoadFromFile("")
	require.NoError(t, err)
}

// --- TLS validation ---

func TestTLSCertOnly(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	t.Setenv("OCG_DI_GRPC__TLS__CERT_FILE", "/tmp/cert.pem")

	_, err := LoadFromFile("")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "tls")
}

func TestTLSAllThreePresent(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	t.Setenv("OCG_DI_GRPC__TLS__CERT_FILE", "/tmp/cert.pem")
	t.Setenv("OCG_DI_GRPC__TLS__KEY_FILE", "/tmp/key.pem")
	t.Setenv("OCG_DI_GRPC__TLS__CA_FILE", "/tmp/ca.pem")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	require.NotNil(t, cfg.GRPC.TLS)
	assert.Equal(t, "/tmp/cert.pem", cfg.GRPC.TLS.CertFile)
}

func TestTLSNoneSet(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Nil(t, cfg.GRPC.TLS)
}

// --- ShutdownTimeout ---

func TestShutdownTimeoutDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.GRPC.ShutdownTimeout)
}

func TestShutdownTimeoutNegative(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	// Negative shutdown timeout via YAML file since duration env parsing is limited
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\n  shutdown_timeout: -1s\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
}

// --- LimitsConfig ---

func TestMaxMessagesDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 1000, cfg.Limits.MaxMessages)
}

func TestMaxMessagesNegative(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nlimits:\n  max_messages: -1\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
}

func TestMaxMessagesExplicit(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv("OCG_DI_LIMITS__MAX_MESSAGES", "500")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 500, cfg.Limits.MaxMessages)
}

func TestMaxPayloadBytesDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, int64(4*1024*1024), cfg.Limits.MaxPayloadBytes)
}

func TestMaxPayloadBytesNegative(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nlimits:\n  max_payload_bytes: -1\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
}

func TestMaxPayloadBytesExplicit(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv("OCG_DI_LIMITS__MAX_PAYLOAD_BYTES", "2097152")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, int64(2097152), cfg.Limits.MaxPayloadBytes)
}

// --- RetryConfig ---

func TestInitialBackoffDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 1*time.Second, cfg.Retry.InitialBackoff)
}

func TestInitialBackoffNegative(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nretry:\n  initial_backoff: -1s\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
}

func TestInitialBackoffExplicit(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nretry:\n  initial_backoff: 5s\n"), 0600))

	cfg, err := LoadFromFile(cfgFile)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Retry.InitialBackoff)
}

func TestMaxBackoffDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, cfg.Retry.MaxBackoff)
}

func TestMaxBackoffLessThanInitial(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nretry:\n  initial_backoff: 10s\n  max_backoff: 5s\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "maxbackoff")
}

func TestMaxBackoffEqualToInitial(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nretry:\n  initial_backoff: 5s\n  max_backoff: 5s\n"), 0600))

	cfg, err := LoadFromFile(cfgFile)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, cfg.Retry.MaxBackoff)
}

func TestMultiplierDefault(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 1.0, cfg.Retry.Multiplier)
}

func TestMultiplierBelowOne(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	t.Setenv("OCG_DI_RETRY__MULTIPLIER", "0.5")

	_, err := LoadFromFile("")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "multiplier")
}

func TestMultiplierExactlyOne(t *testing.T) {
	t.Setenv(grpcHostEnv, "0.0.0.0")
	t.Setenv(grpcPortEnv, "9090")
	t.Setenv(dbPasswordEnv, "testpassword")
	t.Setenv("OCG_DI_RETRY__MULTIPLIER", "1.0")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Equal(t, 1.0, cfg.Retry.Multiplier)
}

// --- YAML file + env override ---

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\n"), 0600))

	t.Setenv(grpcHostEnv, "127.0.0.1")
	t.Setenv(grpcPortEnv, "9091")
	t.Setenv(dbPasswordEnv, "testpassword")

	cfg, err := LoadFromFile(cfgFile)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", cfg.GRPC.Host)
	assert.Equal(t, 9091, cfg.GRPC.Port)
}

// --- Load (wraps LoadFromFile via OCG_DI_CONFIG_FILE) ---

func TestLoad(t *testing.T) {
	t.Setenv(dbPasswordEnv, "testpassword")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.GRPC.Port)
}

func TestLoadFromFileInvalidPath(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/config.yaml")
	require.Error(t, err)
}

// --- validateRetry: negative MaxBackoff ---

func TestMaxBackoffNegative(t *testing.T) {
	t.Setenv(dbPasswordEnv, "testpassword")
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("grpc:\n  host: \"0.0.0.0\"\n  port: 9090\nretry:\n  max_backoff: -1s\n"), 0600))

	_, err := LoadFromFile(cfgFile)
	require.Error(t, err)
}
