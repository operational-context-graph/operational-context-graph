// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromFileDefaults(t *testing.T) {
	cfg, err := LoadFromFile("")
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "info", cfg.Logging.Level)
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
