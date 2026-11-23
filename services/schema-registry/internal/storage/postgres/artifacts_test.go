package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

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
