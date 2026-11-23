package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

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
// Missing current state and lineage are inconsistent; a missing manifest is not found.
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

// TestMigrationDialectRejectsUnknownValues checks Goose dialect validation and restores the PostgreSQL dialect afterward.
// This test stays serial because Goose stores its dialect globally.
func TestMigrationDialectRejectsUnknownValues(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, goose.SetDialect("postgres")) })
	require.Error(t, goose.SetDialect("not-a-dialect"))
	require.NoError(t, goose.SetDialect("postgres"))
}
