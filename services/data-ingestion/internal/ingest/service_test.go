// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoopServicePing(t *testing.T) {
	svc := NewNoopService()
	require.NoError(t, svc.Ping(context.Background()))
}

// Verify NoopService satisfies the Service interface.
var _ Service = (*NoopService)(nil)
