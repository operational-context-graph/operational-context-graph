// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// TestAPIConformanceWithPostgres exercises the HTTP server with a migrated
// PostgreSQL read boundary. Route-conformance tests cover every resource with
// controlled snapshots. This suite verifies the network-to-database boundary.
func TestAPIConformanceWithPostgres(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("schema_registry"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Skipf("PostgreSQL container is unavailable: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	migrationDatabase, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, migrationDatabase.Close()) })

	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, migrationDatabase, filepath.Join("..", "..", "..", "migrations")))

	database, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(database.Close)

	store, err := storage.NewStore(database, storage.Timeouts{
		Acquire: time.Second, Query: time.Second, Statement: time.Second,
		Lock: time.Second, IdleInTransaction: time.Second,
	})
	require.NoError(t, err)
	checker := &poolChecker{pool: database}
	reader := release.NewPostgresReader(store)
	server := httpapi.NewWithServices(apiConfig(), checker, release.New(reader), artifact.New(reader))
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	client := &http.Client{Timeout: time.Second}

	for _, path := range []string{"/healthz", "/readyz"} {
		response := getAPIResponse(t, client, httpServer.URL+path)
		assert.Equal(t, http.StatusOK, response.StatusCode, path)
		require.NoError(t, response.Body.Close())
	}

	response := getAPIResponse(t, client, httpServer.URL+"/v1/registry")
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.Equal(t, "no-cache", response.Header.Get("Cache-Control"))
	assert.Empty(t, response.Header.Get("Schema-Version"))
	require.NoError(t, response.Body.Close())

	server.Drain()
	response = getAPIResponse(t, client, httpServer.URL+"/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.NoError(t, response.Body.Close())

	checker.Close()
	response = getAPIResponse(t, client, httpServer.URL+"/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

type poolChecker struct{ pool *pgxpool.Pool }

func (c *poolChecker) Ready(ctx context.Context) error {
	_, err := c.pool.Exec(ctx, "SELECT 1")
	return err
}

func (c *poolChecker) Close() { c.pool.Close() }

func getAPIResponse(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	response, err := client.Get(url)
	require.NoError(t, err)
	return response
}

func apiConfig() introspection.Config {
	return introspection.Config{
		Server: introspection.ServerConfig{
			Host: "127.0.0.1", Port: 8081, ReadHeaderTimeout: time.Second,
			ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second,
			ShutdownTimeout: time.Second,
		},
		Database:  introspection.DatabaseConfig{QueryTimeout: time.Second},
		Admission: introspection.AdmissionConfig{MaxInFlight: 1, RetryInterval: time.Second},
		Cursor: introspection.CursorConfig{ActiveKey: introspection.CursorKey{
			ID: "test", Secret: string(make([]byte, 32)), Deadline: time.Now().Add(time.Hour),
		}},
	}
}
