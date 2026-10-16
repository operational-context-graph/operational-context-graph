// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package outbox_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/outbox"
)

// startPostgres boots a throwaway PostgreSQL container and returns a connected
// pool. The pool and container are torn down via t.Cleanup.
func startPostgres(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("ocg"),
		postgres.WithUsername("ocg"),
		postgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool, dsn
}

func TestIntegration_MigrateCreatesTable(t *testing.T) {
	pool, _ := startPostgres(t)
	ctx := context.Background()

	require.NoError(t, outbox.Migrate(ctx, pool))

	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'data_ingestion_outbox')`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestIntegration_MigrateIsIdempotent(t *testing.T) {
	pool, _ := startPostgres(t)
	ctx := context.Background()

	require.NoError(t, outbox.Migrate(ctx, pool))
	require.NoError(t, outbox.Migrate(ctx, pool)) // second run is a no-op
}

func TestIntegration_MigrateConcurrentIsSerialized(t *testing.T) {
	pool, _ := startPostgres(t)
	ctx := context.Background()

	const goroutines = 5
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = outbox.Migrate(ctx, pool)
		}(g)
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		require.NoError(t, errs[g])
	}
}

func TestIntegration_WritePersistsRowsAtomically(t *testing.T) {
	pool, _ := startPostgres(t)
	ctx := context.Background()
	require.NoError(t, outbox.Migrate(ctx, pool))

	w := outbox.New(pool)
	rows := []ingest.OutboxRecord{
		{
			TargetStore:         "telemetry",
			LogicalArtifactType: "ocg.telemetry.log",
			SchemaVersion:       "2.4.0",
			Payload:             []byte(`{"msg":"hello"}`),
			ContentType:         "application/json",
		},
		{
			TargetStore:         "inventory",
			LogicalArtifactType: "ocg.inventory.server",
			SchemaVersion:       "1.0.0",
			Payload:             []byte(`{"id":42}`),
			// ContentType empty -> should default to application/json.
		},
	}
	require.NoError(t, w.Write(ctx, rows))

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM data_ingestion_outbox`).Scan(&count))
	assert.Equal(t, 2, count)

	var (
		targetStore  string
		artifactType string
		version      string
		contentType  string
		payload      []byte
		batchID      *int64
	)
	err := pool.QueryRow(ctx,
		`SELECT target_store, logical_artifact_type, schema_version, content_type, payload, batch_id
		   FROM data_ingestion_outbox WHERE target_store = 'telemetry'`).
		Scan(&targetStore, &artifactType, &version, &contentType, &payload, &batchID)
	require.NoError(t, err)
	assert.Equal(t, "telemetry", targetStore)
	assert.Equal(t, "ocg.telemetry.log", artifactType)
	assert.Equal(t, "2.4.0", version)
	assert.Equal(t, "application/json", contentType)
	assert.JSONEq(t, `{"msg":"hello"}`, string(payload))
	assert.Nil(t, batchID, "batch_id must start NULL (pending)")

	// The inventory row's missing content type defaulted.
	var invContentType string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT content_type FROM data_ingestion_outbox WHERE target_store = 'inventory'`).Scan(&invContentType))
	assert.Equal(t, "application/json", invContentType)
}

func TestIntegration_WriteInvalidPayloadLeavesNoRows(t *testing.T) {
	pool, _ := startPostgres(t)
	ctx := context.Background()
	require.NoError(t, outbox.Migrate(ctx, pool))

	w := outbox.New(pool)
	// Non-JSON payload cannot be stored in a jsonb column: the insert must fail
	// and leave no rows (atomic rollback).
	rows := []ingest.OutboxRecord{
		{TargetStore: "telemetry", LogicalArtifactType: "t", SchemaVersion: "1", Payload: []byte(`{"ok":true}`)},
		{TargetStore: "telemetry", LogicalArtifactType: "t", SchemaVersion: "1", Payload: []byte(`not json`)},
	}
	require.Error(t, w.Write(ctx, rows))

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM data_ingestion_outbox`).Scan(&count))
	assert.Equal(t, 0, count, "a failed batch must leave no rows")
}
