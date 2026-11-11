package postgres

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

/*
Pool configuration and readiness unit-test scope

Configuration tests validate transport requirements without opening database connections.
The readiness cancellation test uses net.Pipe and a minimal PostgreSQL startup stub.
The stub authenticates a synthetic connection without contacting a PostgreSQL server.
The driver tracer cancels SELECT 1 after acquisition but before the query reaches the stub.
Assertions distinguish a query-stage failure from an acquisition-stage failure and require zero acquired connections afterward.

The synthetic connection's plaintext transport does not change production Transport Layer Security (TLS) requirements.
Docker-backed lifecycle checks belong to test/integration/postgres/runtime_pool_test.go, not this unit-test file.
The pool closes before the test waits for stub workers, preventing cleanup from racing further connection attempts.
*/

// TestConfigurePoolRequiresVerifiedTLS rejects plaintext configuration without opening a connection.
// Transport Layer Security (TLS) remains mandatory at the pool configuration boundary.
func TestConfigurePoolRequiresVerifiedTLS(t *testing.T) {
	t.Parallel()
	_, err := configurePool("postgres://registry@db.example.test/registry?sslmode=disable", 2, 1)
	require.Error(t, err)
}

// TestConfigurePoolPinsTLS13AndRolePreparation checks TLS 1.3, role preparation, and the fixed search path.
func TestConfigurePoolPinsTLS13AndRolePreparation(t *testing.T) {
	t.Parallel()
	config, err := configurePool("postgres://registry@db.example.test/registry?sslmode=verify-full", 2, 1)
	require.NoError(t, err)
	require.NotNil(t, config.ConnConfig.TLSConfig)
	assert.Equal(t, uint16(tls.VersionTLS13), config.ConnConfig.TLSConfig.MinVersion)
	require.NotNil(t, config.PrepareConn)
	assert.Equal(t, "schema_registry,pg_catalog", config.ConnConfig.RuntimeParams["search_path"])
}

// cancellingProbeTracer cancels only the readiness statement after the pool acquires a connection.
type cancellingProbeTracer struct{ cancel context.CancelFunc }

func (tracer cancellingProbeTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL == "SELECT 1" {
		tracer.cancel()
	}
	return ctx
}

func (cancellingProbeTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestCancelledReadinessQueryReleasesConnection cancels an acquired connection's readiness query through the driver tracer.
// An in-process protocol stub keeps this unit test independent of Docker and PostgreSQL servers.
// The test requires the cancellation cause, the query-stage error, and zero acquired connections afterward.
func TestCancelledReadinessQueryReleasesConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig("postgres://registry@localhost/registry?sslmode=disable")
	require.NoError(t, err)
	config.MaxConns = 1
	config.ConnConfig.Tracer = cancellingProbeTracer{cancel: cancel}
	var servers sync.WaitGroup
	// An in-process PostgreSQL startup stub supplies a real pgx connection.
	// The tracer cancels readiness before its SELECT reaches the wire.
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		servers.Go(func() {
			defer func() { _ = server.Close() }()
			// Assertions stay non-fatal inside workers; the parent owns fatal failure decisions.
			if !assert.NoError(t, server.SetDeadline(time.Now().Add(5*time.Second))) {
				return
			}
			backend := pgproto3.NewBackend(server, server)
			if _, startupErr := backend.ReceiveStartupMessage(); startupErr != nil {
				// Failed startup also makes the parent's readiness assertions fail.
				return
			}
			backend.Send(&pgproto3.AuthenticationOk{})
			backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			if flushErr := backend.Flush(); flushErr != nil {
				return
			}
			// Cancellation may close the pipe without a termination message.
			_, _ = backend.Receive()
		})
		return client, nil
	}
	// Wait only after pool closure prevents further connection attempts.
	defer servers.Wait()
	raw, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	pool := &Pool{pool: raw}
	defer pool.Close()
	err = pool.Ready(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "execute readiness query")
	require.Zero(t, raw.Stat().AcquiredConns())
}

// TestOpenRejectsMalformedConfiguration rejects an invalid database target before constructing a runtime pool.
func TestOpenRejectsMalformedConfiguration(t *testing.T) {
	t.Parallel()
	pool, err := Open(context.Background(), introspection.DatabaseConfig{URL: "://invalid"})
	require.ErrorContains(t, err, "parse database configuration")
	require.Nil(t, pool)
}
