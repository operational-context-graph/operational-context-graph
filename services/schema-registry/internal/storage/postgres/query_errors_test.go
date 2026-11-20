package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

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

func valueRow(values ...any) pgx.Row {
	return queryTestRow(func(dest ...any) error {
		if len(dest) != len(values) {
			return errors.New("test row column count mismatch")
		}
		for i, value := range values {
			target := reflect.ValueOf(dest[i]).Elem()
			if value == nil {
				target.SetZero()
			} else {
				target.Set(reflect.ValueOf(value))
			}
		}
		return nil
	})
}

func sqlContains(fragment string) any {
	return mock.MatchedBy(func(query string) bool { return strings.Contains(query, fragment) })
}

// Successful projection checks ensure each injected failure occurs after the initial database operation.
func expectVerifiedProjection(t *testing.T, tx *queryTestTx, family model.ProjectionFamily) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "manifest", "valid-initial.json"))
	require.NoError(t, err)
	raw, err = jcs.Transform(raw)
	require.NoError(t, err)
	document, err := manifest.Parse(raw)
	require.NoError(t, err)
	expected, err := manifestProjectionRows(document, raw, family)
	if family == model.ProjectionSourceMapping {
		// The initial fixture contains no mappings; the descriptor fallback also returns an empty projection.
		expected, err = []string{}, nil
	}
	require.NoError(t, err)
	query, err := projectionQuery(family)
	require.NoError(t, err)
	rows := &queryTestRows{}
	for _, value := range expected {
		rows.values = append(rows.values, []any{value})
	}
	tx.On("QueryRow", mock.Anything, sqlContains("SELECT EXISTS (SELECT 1 FROM schema_registry.releases"), []any{"1.0.0"}).Return(valueRow(true)).Once()
	tx.On("Query", mock.Anything, query, []any{"1.0.0"}).Return(rows, nil).Once()
	tx.On("QueryRow", mock.Anything, sqlContains("SELECT manifest_bytes"), []any{"1.0.0"}).Return(valueRow(raw)).Once()
	tx.On("QueryRow", mock.Anything, sqlContains("SELECT commitment"), []any{"1.0.0", family}).Return(valueRow(ProjectionCommitment(expected))).Once()
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

// queryTestRows supplies successful rows before an optional scan or iteration failure.
// The closed flag lets tests verify deferred row cleanup.
type queryTestRows struct {
	pgx.Rows
	values  [][]any
	index   int
	scanErr error
	rowErr  error
	closed  bool
}

func (rows *queryTestRows) Next() bool {
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *queryTestRows) Scan(dest ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	return valueRow(rows.values[rows.index-1]...).Scan(dest...)
}
func (rows *queryTestRows) Err() error { return rows.rowErr }
func (rows *queryTestRows) Close()     { rows.closed = true }

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
					rows.values, rows.scanErr, rows.rowErr = [][]any{{}}, cause, nil
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

// TestCollectionFailuresAfterVerifiedProjection checks later queries after visibility and projection checks succeed.
func TestCollectionFailuresAfterVerifiedProjection(t *testing.T) {
	for _, test := range []struct {
		name           string
		family         model.ProjectionFamily
		revisionScoped bool
		query          string
		row            []any
		read           func(context.Context, *Snapshot) error
	}{
		{name: "resources", family: model.ProjectionResource, revisionScoped: true, query: "SELECT resource.artifact_revision", row: []any{"revision", "program.bin", "program", "application/octet-stream", manifestDigest(nil), int64(0)}, read: func(ctx context.Context, s *Snapshot) error {
			items, err := s.Resources(ctx, "revision")
			if err != nil && items != nil {
				return errors.New("partial resource result")
			}
			return err
		}},
		{name: "dependencies", family: model.ProjectionDependency, revisionScoped: true, query: "SELECT dependency.artifact_revision", row: []any{"revision", "schema", "device", "schema-r1"}, read: func(ctx context.Context, s *Snapshot) error {
			items, err := s.Dependencies(ctx, "revision")
			if err != nil && items != nil {
				return errors.New("partial dependency result")
			}
			return err
		}},
		{name: "bindings", family: model.ProjectionBinding, query: "SELECT binding.schema_version", row: []any{"1.0.0", "schema-r1", "context-r1"}, read: func(ctx context.Context, s *Snapshot) error {
			items, err := s.ContextBindings(ctx, "1.0.0", "schema-r1")
			if err != nil && items != nil {
				return errors.New("partial binding result")
			}
			return err
		}},
		{name: "stores", family: model.ProjectionStorage, query: "SELECT entry.schema_version, entry.artifact_type", row: []any{"1.0.0", "device", "data-center-inventory"}, read: func(ctx context.Context, s *Snapshot) error {
			items, err := s.StorageEntries(ctx, "1.0.0", "device")
			if err != nil && items != nil {
				return errors.New("partial store result")
			}
			return err
		}},
	} {
		for _, phase := range []string{"query", "scan", "iteration after row"} {
			t.Run(test.name+"/"+phase, func(t *testing.T) {
				cause := errors.New("downstream database failure")
				tx := new(queryTestTx)
				tx.Test(t)
				if test.revisionScoped {
					tx.On("QueryRow", mock.Anything, sqlContains("SELECT EXISTS (SELECT 1 FROM schema_registry.activated_revision_reachability"), []any{"revision"}).Return(valueRow(true)).Once()
					tx.On("Query", mock.Anything, sqlContains("SELECT DISTINCT selection.schema_version"), []any{"revision"}).Return(&queryTestRows{values: [][]any{{"1.0.0"}}}, nil).Once()
				}
				expectVerifiedProjection(t, tx, test.family)
				rows := &queryTestRows{values: [][]any{test.row}}
				if phase == "query" {
					tx.On("Query", mock.Anything, sqlContains(test.query), mock.Anything).Return(nil, cause).Once()
				} else {
					if phase == "scan" {
						rows.scanErr = cause
					} else {
						rows.rowErr = cause
					}
					tx.On("Query", mock.Anything, sqlContains(test.query), mock.Anything).Return(rows, nil).Once()
				}
				err := test.read(t.Context(), &Snapshot{tx: tx})
				require.ErrorIs(t, err, cause)
				if phase != "query" {
					assert.True(t, rows.closed)
				}
				tx.AssertExpectations(t)
			})
		}
	}
}

// TestMappingTransformationFailuresAfterVerifiedProjection checks later database failures and invalid transformation structure.
func TestMappingTransformationFailuresAfterVerifiedProjection(t *testing.T) {
	for _, phase := range []string{"detail", "query", "scan", "iteration after row", "ordinal gap", "empty"} {
		t.Run(phase, func(t *testing.T) {
			cause := errors.New("mapping query failed")
			tx := new(queryTestTx)
			tx.Test(t)
			expectVerifiedProjection(t, tx, model.ProjectionSourceMapping)
			detail := valueRow("1.0.0", "mapping-r1", "source", "source-r1", "1.0.0", "opaque", "1.0.0", "test", "", "")
			if phase == "detail" {
				detail = failingRow(cause)
			}
			tx.On("QueryRow", mock.Anything, sqlContains("SELECT detail.schema_version"), []any{"1.0.0", "mapping-r1"}).Return(detail).Once()
			rows := &queryTestRows{values: [][]any{{0, "target", "target-r1", "one", "program.bin", manifestDigest(nil)}}}
			if phase != "detail" {
				if phase == "query" {
					tx.On("Query", mock.Anything, sqlContains("SELECT ordinal"), mock.Anything).Return(nil, cause).Once()
				} else {
					switch phase {
					case "scan":
						rows.scanErr = cause
					case "iteration after row":
						rows.rowErr = cause
					case "ordinal gap":
						rows.values[0][0] = 1
					case "empty":
						rows.values = nil
					}
					tx.On("Query", mock.Anything, sqlContains("SELECT ordinal"), mock.Anything).Return(rows, nil).Once()
				}
			}
			value, err := (&Snapshot{tx: tx}).SourceMappingByRevision(t.Context(), "1.0.0", "mapping-r1")
			want := cause
			if phase == "ordinal gap" || phase == "empty" {
				want = ErrInconsistentState
			}
			require.ErrorIs(t, err, want)
			assert.Empty(t, value.Transformations)
			if phase != "detail" && phase != "query" {
				assert.True(t, rows.closed)
			}
			tx.AssertExpectations(t)
		})
	}
}

func TestArtifactProjectionRejectsMalformedExtensionRows(t *testing.T) {
	for _, value := range []string{"missing-fields", strings.Repeat(":", 10) + "zz", strings.Repeat(":", 10) + "7b"} {
		tx := new(queryTestTx)
		tx.Test(t)
		rows := &queryTestRows{values: [][]any{{value}}}
		query, err := projectionQuery(model.ProjectionArtifact)
		require.NoError(t, err)
		tx.On("Query", mock.Anything, query, []any{"1.0.0"}).Return(rows, nil).Once()
		items, err := projectionRows(t.Context(), tx, "1.0.0", model.ProjectionArtifact)
		require.ErrorIs(t, err, ErrInconsistentState)
		assert.Nil(t, items)
		assert.True(t, rows.closed)
		tx.AssertExpectations(t)
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
