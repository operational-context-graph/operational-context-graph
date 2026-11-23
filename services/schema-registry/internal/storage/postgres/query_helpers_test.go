package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// Query exposes mocked multirow operations for precise query, scan, and iteration failure injection.
func (tx *queryTestTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	result := tx.Called(ctx, sql, args)
	rows, _ := result.Get(0).(pgx.Rows)
	return rows, result.Error(1)
}

// QueryRow supplies mocked single-row results for missing-row and classification tests.
func (tx *queryTestTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.Called(ctx, sql, args).Get(0).(pgx.Row)
}

// Exec supplies mocked command results for transaction-local timeout setup tests.
func (tx *queryTestTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	result := tx.Called(ctx, sql, args)
	return result.Get(0).(pgconn.CommandTag), result.Error(1)
}

// queryTestRow controls destination values or scan failures without PostgreSQL.
type queryTestRow func(...any) error

// Scan delegates destination handling to the row's injected success or failure behavior.
func (row queryTestRow) Scan(dest ...any) error { return row(dest...) }

// failingRow returns a row whose scan preserves the supplied error identity.
func failingRow(err error) pgx.Row {
	return queryTestRow(func(...any) error { return err })
}

// valueRow supplies typed values to scan destinations for controlled storage metadata fixtures.
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

// sqlContains matches a relevant query fragment without coupling mocks to complete SQL formatting.
func sqlContains(fragment string) any {
	return mock.MatchedBy(func(query string) bool { return strings.Contains(query, fragment) })
}

// expectVerifiedProjection supplies matching authoritative rows so injected failures exercise later database operations.
func expectVerifiedProjection(t *testing.T, tx *queryTestTx, family model.ProjectionFamily) *queryTestRows {
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
	return rows
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

// Next exposes fixture rows in sequence until exhaustion, independently of the
// terminal failure reported by Err.
func (rows *queryTestRows) Next() bool {
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

// Scan copies the current fixture row or returns the configured scan failure.
func (rows *queryTestRows) Scan(dest ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	return valueRow(rows.values[rows.index-1]...).Scan(dest...)
}

// Err exposes the injected terminal iteration failure.
func (rows *queryTestRows) Err() error { return rows.rowErr }

// Close records cleanup so tests can detect leaked row cursors.
func (rows *queryTestRows) Close() { rows.closed = true }
