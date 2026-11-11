package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

/*
Snapshot and storage test conventions

These tests follow the repository's coding-guidelines skill and the Google Go Style Guide.
Assertions and mocks MUST use testify.
Prerequisites that prevent meaningful later assertions MUST use require.
Worker goroutines MUST NOT call fatal assertions.
Workers SHOULD return results for assertions in the parent test.
Table cases SHOULD name their fields and describe the behavior under test.
Assertion helpers MUST mark themselves with t.Helper() when they accept a testing handle.
Comments SHOULD explain fixtures, invariants, and non-obvious synchronization.
Tests MUST join workers and close owned resources, including failure paths.
Shared-state contracts include race-free concurrent access.
The module's coverage requirement remains at least 90 percent.

Storage error-contract scope

These unit tests require neither Docker nor an external PostgreSQL server.
Pure tests use t.Parallel() only when they own their inputs and change no global configuration.
The Goose dialect test stays serial because Goose stores its dialect globally.

queryTestTx mocks supported transaction operations through testify.
queryTestRow supplies scan destinations or a scan error.
queryTestRows injects first-row scan failures or iteration failures before any rows arrive.
Its closed flag verifies resource cleanup.
Missing required read-model records are inconsistent state; missing selectable data is not found.
Each failure must preserve its underlying cause rather than replacing it with an unrelated error.
*/

// queryTestTx mocks only the transaction operations used by these read tests.
// Unimplemented embedded methods fail immediately if a test calls them.
type queryTestTx struct {
	pgx.Tx
	mock.Mock
}

func (tx *queryTestTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	result := tx.Called(ctx, sql, args)
	rows, _ := result.Get(0).(pgx.Rows)
	return rows, result.Error(1)
}

func (tx *queryTestTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.Called(ctx, sql, args).Get(0).(pgx.Row)
}

func (tx *queryTestTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	result := tx.Called(ctx, sql, args)
	return result.Get(0).(pgconn.CommandTag), result.Error(1)
}

// queryTestRow controls destination values or scan failures without PostgreSQL.
type queryTestRow func(...any) error

func (row queryTestRow) Scan(dest ...any) error { return row(dest...) }

// failingRow returns a row whose scan preserves the supplied error identity.
func failingRow(err error) pgx.Row {
	return queryTestRow(func(...any) error { return err })
}

// TestSnapshotQueryFailuresPreserveCause checks errors from each read entry point's first database operation.
// Every case injects one cause and verifies that error wrapping preserves it.
func TestSnapshotQueryFailuresPreserveCause(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		read func(context.Context, *Snapshot) error
	}{
		{name: "current release", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.CurrentRelease(ctx)
			return err
		}},
		{name: "release", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.Release(ctx, "1.0.0")
			return err
		}},
		{name: "releases", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ReleasesAt(ctx, nil, 1, 1)
			return err
		}},
		{name: "lineage", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.IsAncestor(ctx, "1.0.0", "2.0.0")
			return err
		}},
		{name: "artifact", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.Artifact(ctx, "revision")
			return err
		}},
		{name: "selected artifact", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ReleaseArtifact(ctx, "1.0.0", "schema", "device")
			return err
		}},
		{name: "selected metadata", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ReleaseArtifactMetadata(ctx, "1.0.0", "schema", "device")
			return err
		}},
		{name: "candidates", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ReleaseArtifacts(ctx, "1.0.0", model.ArtifactFilter{}, nil, nil, 1)
			return err
		}},
		{name: "history", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ArtifactHistory(ctx, "schema", "device", nil, 1, 1)
			return err
		}},
		{name: "resources", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.Resources(ctx, "revision")
			return err
		}},
		{name: "resource", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.Resource(ctx, "revision", "program")
			return err
		}},
		{name: "dependencies", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.Dependencies(ctx, "revision")
			return err
		}},
		{name: "contexts", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.ContextBindings(ctx, "1.0.0", "revision")
			return err
		}},
		{name: "URI", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.URIEntry(ctx, "1.0.0", "https://example.test/schema")
			return err
		}},
		{name: "stores", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.StorageEntries(ctx, "1.0.0", "device")
			return err
		}},
		{name: "mapping", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.SourceMapping(ctx, "1.0.0", "mapping")
			return err
		}},
		{name: "mapping revision", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.SourceMappingByRevision(ctx, "1.0.0", "revision")
			return err
		}},
		{name: "commitment", read: func(ctx context.Context, s *Snapshot) error {
			return s.VerifyCommitment(ctx, "1.0.0", model.ProjectionArtifact, nil)
		}},
		{name: "manifest bytes", read: func(ctx context.Context, s *Snapshot) error {
			_, err := releaseManifestBytes(ctx, s.tx, "1.0.0")
			return err
		}},
		{name: "reachability", read: func(ctx context.Context, s *Snapshot) error {
			return requireReachableArtifact(ctx, s.tx, "revision")
		}},
		{name: "current pointer", read: func(ctx context.Context, s *Snapshot) error {
			_, err := readCurrentState(ctx, s.tx)
			return err
		}},
		{name: "artifact rows", read: func(ctx context.Context, s *Snapshot) error {
			_, err := scanArtifacts(ctx, s.tx, "SELECT artifact")
			return err
		}},
		{name: "projection rows", read: func(ctx context.Context, s *Snapshot) error {
			_, err := projectionRows(ctx, s.tx, "1.0.0", model.ProjectionArtifact)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := errors.New("database read failed")
			tx := new(queryTestTx)
			tx.Test(t)
			// Different entry points start with either a row query or a row-set query.
			// These cases fail the first operation, not every later query within a compound read.
			tx.On("QueryRow", mock.Anything, mock.Anything, mock.Anything).Return(failingRow(cause)).Maybe()
			tx.On("Query", mock.Anything, mock.Anything, mock.Anything).Return(nil, cause).Maybe()
			err := test.read(context.Background(), &Snapshot{tx: tx})
			require.ErrorIs(t, err, cause)
			require.NotEmpty(t, tx.Calls, "the failure must come from a database operation")
		})
	}
}

// TestStorageMissingRowsHaveStableClassification distinguishes missing data from broken required read-model records.
// Missing current state, lineage, and commitments are inconsistent; a missing manifest is not found.
func TestStorageMissingRowsHaveStableClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		read func(context.Context, *Snapshot) error
		want error
	}{
		{name: "missing current row", read: func(ctx context.Context, s *Snapshot) error {
			_, err := readCurrentState(ctx, s.tx)
			return err
		}, want: ErrInconsistentState},
		{name: "missing lineage", read: func(ctx context.Context, s *Snapshot) error {
			_, err := s.IsAncestor(ctx, "1.0.0", "2.0.0")
			return err
		}, want: ErrInconsistentState},
		{name: "missing commitment", read: func(ctx context.Context, s *Snapshot) error {
			return s.VerifyCommitment(ctx, "1.0.0", model.ProjectionArtifact, nil)
		}, want: ErrInconsistentState},
		{name: "missing manifest", read: func(ctx context.Context, s *Snapshot) error {
			_, err := releaseManifestBytes(ctx, s.tx, "1.0.0")
			return err
		}, want: ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := new(queryTestTx)
			tx.Test(t)
			tx.On("QueryRow", mock.Anything, mock.Anything, mock.Anything).Return(failingRow(pgx.ErrNoRows)).Once()
			require.ErrorIs(t, test.read(context.Background(), &Snapshot{tx: tx}), test.want)
			tx.AssertExpectations(t)
		})
	}
}

// TestCurrentStateRejectsInvalidPointerCoordinates covers absent, inactive, zero-sequence, and valid current pointers.
// Valid state retains the independent history ceiling; invalid state returns no partial coordinates.
func TestCurrentStateRejectsInvalidPointerCoordinates(t *testing.T) {
	t.Parallel()
	version, zero, positive := "1.0.0", int64(0), int64(1)
	for _, test := range []struct {
		name     string
		version  *string
		sequence *int64
		want     error
	}{
		{name: "no active release", want: ErrNoActiveRelease},
		{name: "inactive pointer", version: &version, want: ErrInconsistentState},
		{name: "zero sequence", version: &version, sequence: &zero, want: ErrInconsistentState},
		{name: "valid pointer", version: &version, sequence: &positive},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := new(queryTestTx)
			tx.Test(t)
			tx.On("QueryRow", mock.Anything, mock.Anything, mock.Anything).Return(queryTestRow(func(dest ...any) error {
				require.Len(t, dest, 3)
				*dest[0].(**string) = test.version
				*dest[1].(**int64) = test.sequence
				*dest[2].(*int64) = 3
				return nil
			})).Once()
			state, err := readCurrentState(context.Background(), tx)
			require.ErrorIs(t, err, test.want)
			if test.want == nil {
				require.Equal(t, model.CurrentState{SchemaVersion: version, ActivationSequence: 1, SnapshotSequence: 3}, state)
			} else {
				require.Empty(t, state)
			}
			tx.AssertExpectations(t)
		})
	}
}

// TestScanHelpersClassifyErrors checks artifact and release row scanners without a database.
// Scanners translate absent rows to not-found errors and preserve other scan failures.
func TestScanHelpersClassifyErrors(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{pgx.ErrNoRows, errors.New("invalid column type")} {
		want := cause
		if errors.Is(cause, pgx.ErrNoRows) {
			want = ErrNotFound
		}
		_, err := scanArtifact(failingRow(cause))
		assert.ErrorIs(t, err, want)
		_, err = scanRelease(failingRow(cause))
		assert.ErrorIs(t, err, want)
		_, err = scanReleaseRow(failingRow(cause))
		assert.ErrorIs(t, err, want)
	}
}

// TestNewStoreRejectsMissingPoolAndInvalidTimeouts verifies constructor failures before connection acquisition.
func TestNewStoreRejectsMissingPoolAndInvalidTimeouts(t *testing.T) {
	t.Parallel()
	_, err := NewStore(nil, Timeouts{})
	require.ErrorContains(t, err, "pool is required")
	_, err = NewStore(new(pgxpool.Pool), Timeouts{})
	require.ErrorContains(t, err, "timeouts must be positive")
}

// TestLocalTimeoutFailuresPreserveCause injects a statement-timeout configuration failure.
// The returned error preserves the deadline cause and identifies the failed setting.
func TestLocalTimeoutFailuresPreserveCause(t *testing.T) {
	t.Parallel()
	tx := new(queryTestTx)
	tx.Test(t)
	cause := context.DeadlineExceeded
	tx.On("Exec", mock.Anything, "SELECT pg_catalog.set_config($1, $2, true)", []any{"statement_timeout", "1000"}).Return(pgconn.CommandTag{}, cause).Once()
	err := setLocalTimeouts(context.Background(), tx, Timeouts{Statement: time.Second})
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "statement_timeout")
	tx.AssertExpectations(t)
}

// queryTestRows injects a first-row scan failure or an iteration failure before any rows arrive.
// The closed flag lets tests verify deferred row cleanup.
type queryTestRows struct {
	pgx.Rows
	next    bool
	scanErr error
	rowErr  error
	closed  bool
}

func (rows *queryTestRows) Next() bool {
	next := rows.next
	rows.next = false
	return next
}

func (rows *queryTestRows) Scan(...any) error { return rows.scanErr }
func (rows *queryTestRows) Err() error        { return rows.rowErr }
func (rows *queryTestRows) Close()            { rows.closed = true }

// TestRowIterationFailuresCloseRowsAndRejectPartialResults checks scanner and iterator failures for collection readers.
// Each failure preserves its cause, returns no items, and closes the row set.
func TestRowIterationFailuresCloseRowsAndRejectPartialResults(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		read func(*testing.T, context.Context, pgx.Tx) error
	}{
		{name: "artifacts", read: func(t *testing.T, ctx context.Context, tx pgx.Tx) error {
			t.Helper()
			items, err := scanArtifacts(ctx, tx, "SELECT artifact")
			require.Nil(t, items)
			return err
		}},
		{name: "projections", read: func(t *testing.T, ctx context.Context, tx pgx.Tx) error {
			t.Helper()
			items, err := projectionRows(ctx, tx, "1.0.0", model.ProjectionResource)
			require.Nil(t, items)
			return err
		}},
		{name: "revision selections", read: func(_ *testing.T, ctx context.Context, tx pgx.Tx) error {
			return (&Snapshot{tx: tx}).verifyRevisionProjection(ctx, "revision", model.ProjectionResource)
		}},
		{name: "history", read: func(t *testing.T, ctx context.Context, tx pgx.Tx) error {
			t.Helper()
			items, err := (&Snapshot{tx: tx}).ArtifactHistory(ctx, "schema", "device", nil, 1, 1)
			require.Nil(t, items)
			return err
		}},
	} {
		for _, phase := range []string{"scan", "iteration"} {
			t.Run(test.name+"/"+phase, func(t *testing.T) {
				cause := errors.New("row failure")
				// Iteration failures arrive before any rows; scan failures occur on the first row.
				rows := &queryTestRows{rowErr: cause}
				if phase == "scan" {
					rows.next, rows.scanErr, rows.rowErr = true, cause, nil
				}
				tx := new(queryTestTx)
				tx.Test(t)
				tx.On("Query", mock.Anything, mock.Anything, mock.Anything).Return(rows, nil).Once()
				require.ErrorIs(t, test.read(t, context.Background(), tx), cause)
				require.True(t, rows.closed)
				tx.AssertExpectations(t)
			})
		}
	}
}

// TestProjectionRowsRejectsUnknownFamilyBeforeQuery rejects unsupported projection families without executing database queries.
func TestProjectionRowsRejectsUnknownFamilyBeforeQuery(t *testing.T) {
	t.Parallel()
	tx := new(queryTestTx)
	tx.Test(t)
	rows, err := projectionRows(context.Background(), tx, "1.0.0", model.ProjectionFamily("unknown"))
	require.ErrorContains(t, err, "unknown projection family")
	require.Nil(t, rows)
	require.Empty(t, tx.Calls)
}

// TestMigrationDialectRejectsUnknownValues checks Goose dialect validation and restores the PostgreSQL dialect afterward.
// This test stays serial because Goose stores its dialect globally.
func TestMigrationDialectRejectsUnknownValues(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, SetMigrationDialect("postgres")) })
	require.Error(t, SetMigrationDialect("not-a-dialect"))
	require.NoError(t, SetMigrationDialect("postgres"))
}
