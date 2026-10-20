// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package outbox_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
)

func TestMigrate_UnreachableDatabaseFails(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://ocg:secret@127.0.0.1:1/ocg?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	defer pool.Close()

	require.Error(t, outbox.Migrate(ctx, pool))
}
