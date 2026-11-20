package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// cancelQueryOnce injects a canceled driver context at one transaction-boundary statement.
// Later reads use an unmodified context, proving that cleanup permits recovery.
type cancelQueryOnce struct {
	prefix string
	used   atomic.Bool
}

// TraceQueryStart cancels exactly one matching boundary statement while leaving subsequent recovery reads unaffected.
func (tracer *cancelQueryOnce) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, tracer.prefix) && tracer.used.CompareAndSwap(false, true) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}

// TraceQueryEnd completes the tracer contract without altering transaction cleanup evidence.
func (*cancelQueryOnce) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestReadBoundaryFailuresReleaseConnectionsAndRecover verifies begin, timeout-setup, callback, and commit failures preserve causes, release connections, and permit later reads.
func (s *readStoreSuite) TestReadBoundaryFailuresReleaseConnectionsAndRecover() {
	t := s.T()
	fixture := registryFixture{
		version: "1.0.0",
		artifacts: []artifact.Artifact{
			fixtureSchema("device", "device-r1", "1.0.0", []byte("true"), nil),
		},
	}
	_, err := loadValidatedFixture(t, s.Context, s.Database, fixture)
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(s.Context, s.Database, "1.0.0", map[string]int64{"1.0.0": 1}))
	for _, test := range []struct{ name, prefix, message string }{
		{name: "begin", prefix: "begin", message: "begin read transaction"},
		{name: "timeouts", prefix: "SELECT pg_catalog.set_config", message: "set local statement_timeout"},
		{name: "commit", prefix: "commit", message: "commit read transaction"},
		{name: "callback", message: "callback failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			config, err := pgxpool.ParseConfig(s.DatabaseURL)
			require.NoError(t, err)
			tracer := &cancelQueryOnce{prefix: test.prefix}
			if test.prefix != "" {
				config.ConnConfig.Tracer = tracer
			}
			pool, err := pgxpool.NewWithConfig(ctx, config)
			require.NoError(t, err)
			defer pool.Close()
			store, err := storage.NewStore(pool, storage.Timeouts{
				Acquire:           time.Second,
				Query:             3 * time.Second,
				Statement:         time.Second,
				Lock:              time.Second,
				IdleInTransaction: time.Second,
			})
			require.NoError(t, err)
			callbackCause := errors.New("callback failed")
			callbackCalled := false
			err = store.Read(ctx, func(context.Context, *storage.Snapshot) error {
				callbackCalled = true
				if test.name == "callback" {
					return callbackCause
				}
				return nil
			})
			require.ErrorContains(t, err, test.message)
			if test.name == "callback" {
				require.ErrorIs(t, err, callbackCause)
			} else {
				require.ErrorIs(t, err, context.Canceled)
				assert.True(t, tracer.used.Load())
			}
			assert.Equal(t, test.name == "commit" || test.name == "callback", callbackCalled)
			require.Eventually(t, func() bool { return pool.Stat().AcquiredConns() == 0 }, time.Second, time.Millisecond)
			require.NoError(t, store.Read(ctx, func(readCtx context.Context, snapshot *storage.Snapshot) error {
				_, err := snapshot.CurrentRelease(readCtx)
				return err
			}))
			require.Eventually(t, func() bool { return pool.Stat().AcquiredConns() == 0 }, time.Second, time.Millisecond)
		})
	}
}

// TestBlockedReadTimeoutAndCancellationReleaseConnections verifies lock-blocked queries distinguish server timeout from client cancellation, release capacity, and recover after unlocking.
func (s *readStoreSuite) TestBlockedReadTimeoutAndCancellationReleaseConnections() {
	t := s.T()
	_, err := loadValidatedFixture(t, s.Context, s.Database, registryFixture{
		version: "1.0.0",
		artifacts: []artifact.Artifact{
			fixtureSchema("device", "device-r1", "1.0.0", []byte("true"), nil)},
	})
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(s.Context, s.Database, "1.0.0", map[string]int64{"1.0.0": 1}))
	for _, mode := range []string{"statement timeout", "client cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			config, err := pgxpool.ParseConfig(s.DatabaseURL)
			require.NoError(t, err)
			config.ConnConfig.RuntimeParams["application_name"] = "schema-registry-blocked-read-test"
			pool, err := pgxpool.NewWithConfig(ctx, config)
			require.NoError(t, err)
			defer pool.Close()
			statement := 5 * time.Second
			if mode == "statement timeout" {
				statement = 100 * time.Millisecond
			}
			store, err := storage.NewStore(pool, storage.Timeouts{
				Acquire:           5 * time.Second,
				Query:             5 * time.Second,
				Statement:         statement,
				Lock:              5 * time.Second,
				IdleInTransaction: 5 * time.Second,
			})
			require.NoError(t, err)
			lock, err := s.Database.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = lock.Rollback() }()
			_, err = lock.ExecContext(ctx, "LOCK TABLE schema_registry.artifacts IN ACCESS EXCLUSIVE MODE")
			require.NoError(t, err)
			readCtx, cancelRead := context.WithCancel(ctx)
			defer cancelRead()
			results := make(chan error, 1)
			go func() {
				results <- store.Read(readCtx, func(ctx context.Context, snapshot *storage.Snapshot) error {
					_, readErr := snapshot.Artifact(ctx, "device-r1")
					return readErr
				})
			}()
			// Join the worker before closing its pool, even when a parent assertion fails.
			joined := false
			defer func() {
				cancelRead()
				if !joined {
					assert.Error(t, <-results)
				}
			}()
			if mode == "client cancellation" {
				require.Eventually(t, func() bool {
					var blocked bool
					queryErr := s.Database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE application_name = 'schema-registry-blocked-read-test' AND wait_event_type = 'Lock')`).Scan(&blocked)
					return queryErr == nil && blocked
				}, 3*time.Second, 10*time.Millisecond)
				cancelRead()
			}
			select {
			case err = <-results:
				joined = true
			case <-ctx.Done():
				require.FailNow(t, "blocked read did not return before its deadline")
			}
			require.Error(t, err)
			if mode == "statement timeout" {
				var postgresError *pgconn.PgError
				require.ErrorAs(t, err, &postgresError)
				assert.Equal(t, "57014", postgresError.Code)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.Eventually(t, func() bool { return pool.Stat().AcquiredConns() == 0 }, time.Second, time.Millisecond)
			require.NoError(t, lock.Rollback())
			require.NoError(t, store.Read(ctx, func(ctx context.Context, snapshot *storage.Snapshot) error {
				_, err := snapshot.Artifact(ctx, "device-r1")
				return err
			}))
			assert.Zero(t, pool.Stat().AcquiredConns())
		})
	}
}
