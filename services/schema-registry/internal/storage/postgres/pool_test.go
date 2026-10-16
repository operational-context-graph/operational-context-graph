// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurePoolRequiresVerifiedTLS(t *testing.T) {
	t.Parallel()
	_, err := configurePool("postgres://registry:password@db.example.test/registry?sslmode=disable", 2, 1)
	require.Error(t, err)
}

func TestConfigurePoolPinsTLS13AndRolePreparation(t *testing.T) {
	t.Parallel()
	config, err := configurePool("postgres://registry:password@db.example.test/registry?sslmode=verify-full", 2, 1)
	require.NoError(t, err)
	require.NotNil(t, config.ConnConfig.TLSConfig)
	assert.Equal(t, uint16(tls.VersionTLS13), config.ConnConfig.TLSConfig.MinVersion)
	require.NotNil(t, config.PrepareConn)
}
