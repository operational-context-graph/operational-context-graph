package httpapi_test

import (
	"context"
	"database/sql"
	"net"
	"net/http"
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

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// observingReader decorates a production release.Reader with one deterministic synchronization point.
// Before the Read method calls the wrapped reader, it closes the started channel. The test waits for
// that channel closure to confirm that the HTTP request entered the read boundary before it cancels
// server shutdown.
//
// This helper does not alter snapshot creation, query execution, errors, or transaction behavior.
// It exists because the database connection pool (pgxpool) does not expose a notification for the
// instant a goroutine starts waiting for an unavailable connection.
type observingReader struct {
	reader  release.Reader
	started chan struct{}
}

func (r observingReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	// This test starts exactly one registry request. Therefore closing the started
	// channel is safe and makes accidental additional read boundaries fail loudly.
	close(r.started)
	return r.reader.Read(ctx, fn)
}

// TestShutdownCancelsBlockedPoolAcquisition verifies the PostgreSQL portion of the graceful shutdown.
// The test migrates an empty database, creates a real pgxpool.Pool with one connection, and acquires
// that connection in the test. A registry request then reads from the store and blocks while attempting
// to acquire the unavailable pool connection.
//
// After observing the reader boundary, the test cancels the listener context to model SIGTERM after
// command-level signal handling. CanceledAcquireCount proves that the pending database connection pool
// acquire observed the canceled request context. The held connection is released so the server can
// complete its bounded drain and close the pool. The final assertions verify no acquired connection
// remains and that the read-only request did not insert releases. No release fixture is needed because
// acquisition occurs before the first registry-state query.
func TestShutdownCancelsBlockedPoolAcquisition(t *testing.T) {
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

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	poolConfig.MaxConns = 1
	database, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err)
	checker := &poolChecker{pool: database}
	store, err := storage.NewStore(database, storage.Timeouts{
		Acquire: time.Second, Query: time.Second, Statement: time.Second,
		Lock: time.Second, IdleInTransaction: time.Second,
	})
	require.NoError(t, err)
	reader := observingReader{reader: release.NewPostgresReader(store), started: make(chan struct{})}
	server := httpapi.NewWithServices(apiConfig(), checker, release.New(reader), artifact.New(reader))

	held, err := database.Acquire(ctx)
	require.NoError(t, err)
	canceledAcquireCount := database.Stat().CanceledAcquireCount()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	shutdownCtx, sigterm := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunListener(shutdownCtx, listener) }()

	client := &http.Client{Timeout: time.Second}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		// Shutdown can complete an error response or close the transport; only completion matters here.
		response, _ := client.Get("http://" + listener.Addr().String() + "/v1/registry")
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("registry request did not reach the PostgreSQL read boundary")
	}

	sigterm()
	assert.Eventually(t, func() bool {
		return database.Stat().CanceledAcquireCount() > canceledAcquireCount
	}, time.Second, time.Millisecond, "shutdown must cancel the pending acquire")
	held.Release()
	require.NoError(t, <-done)
	<-requestDone
	assert.Zero(t, database.Stat().AcquiredConns(), "shutdown must not leak pool connections")

	var releaseCount int
	require.NoError(t, migrationDatabase.QueryRowContext(ctx, "SELECT count(*) FROM schema_registry.releases").Scan(&releaseCount))
	assert.Zero(t, releaseCount, "read shutdown handling must not mutate registry state")
}
