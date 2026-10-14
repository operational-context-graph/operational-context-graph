// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUsesEnvConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  port: 7070\n"), 0o600))
	t.Setenv("OCG_DI_CONFIG_FILE", path)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 7070, cfg.Server.Port)
}

func TestLoadFromFileReadsOverridesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("logging:\n  level: warn\n"), 0o600))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "warn", cfg.Logging.Level)
}

func TestLoadFromFileMissingFileFails(t *testing.T) {
	_, err := LoadFromFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
}

func TestDatabaseDSNWithoutMaxConns(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_MAXCONNS", "0")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	dsn := cfg.DatabaseDSN()
	assert.NotContains(t, dsn, "pool_max_conns")
}

func TestLoadFromFileDefaults(t *testing.T) {
	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, 5432, cfg.Database.Port)
	assert.Equal(t, "ocg", cfg.Database.Name)
	assert.Equal(t, "outbox", cfg.Database.OutboxTable)
}

func TestLoadFromFileEnvOverride(t *testing.T) {
	t.Setenv("OCG_DI_SERVER_PORT", "9090")
	t.Setenv("OCG_DI_LOGGING_LEVEL", "debug")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Logging.Level)
}

func TestLoadFromFileInvalidLogLevel(t *testing.T) {
	t.Setenv("OCG_DI_LOGGING_LEVEL", "verbose")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileValidDatabaseConfig(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_HOST", "db.internal")
	t.Setenv("OCG_DI_DATABASE_NAME", "ocg_prod")
	t.Setenv("OCG_DI_DATABASE_USER", "ingestor")
	t.Setenv("OCG_DI_DATABASE_PASSWORD", "s3cret")

	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, "db.internal", cfg.Database.Host)
	assert.Equal(t, "ocg_prod", cfg.Database.Name)
	assert.Equal(t, "ingestor", cfg.Database.User)
	assert.Equal(t, "s3cret", cfg.Database.Password)
}

func TestLoadFromFileMissingDatabaseHostFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_HOST", "")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileMissingDatabaseNameFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_NAME", "")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestLoadFromFileInvalidDatabasePortFails(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_PORT", "0")

	_, err := LoadFromFile("")
	require.Error(t, err)
}

func TestDatabaseDSNContainsCredentialsAndParams(t *testing.T) {
	t.Setenv("OCG_DI_DATABASE_HOST", "db.internal")
	t.Setenv("OCG_DI_DATABASE_NAME", "ocg_prod")
	t.Setenv("OCG_DI_DATABASE_USER", "ingestor")
	t.Setenv("OCG_DI_DATABASE_PASSWORD", "p@ss/word")

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
}

func TestPasswordNotReadFromConfigFile(t *testing.T) {
	// A config file sets DB fields but MUST NOT be the source of the password.
	// Only OCG_DI_DATABASE_PASSWORD supplies it.
	cfg, err := LoadFromFile("")
	require.NoError(t, err)
	assert.Empty(t, cfg.Database.Password)
}

