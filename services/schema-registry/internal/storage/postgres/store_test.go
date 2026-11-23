package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

// TestTimeoutsValidate verifies positive database timeouts pass and a disabled timeout fails validation.
func TestTimeoutsValidate(t *testing.T) {
	t.Parallel()
	valid := Timeouts{
		Acquire: time.Second, Query: time.Second, Statement: time.Second,
		Lock: time.Second, IdleInTransaction: time.Second,
	}
	require.NoError(t, valid.validate())

	valid.Query = 0
	assert.Error(t, valid.validate())
}

// TestTimeoutsCapToRequestDeadline verifies statement, lock, and idle-transaction timeouts cannot exceed the remaining request budget.
func TestTimeoutsCapToRequestDeadline(t *testing.T) {
	t.Parallel()
	timeouts := Timeouts{Acquire: time.Second, Query: time.Second, Statement: 5 * time.Second, Lock: 3 * time.Second, IdleInTransaction: 2 * time.Second}
	capped := timeouts.capped(500 * time.Millisecond)
	assert.Equal(t, 500*time.Millisecond, capped.Statement)
	assert.Equal(t, 500*time.Millisecond, capped.Lock)
	assert.Equal(t, 500*time.Millisecond, capped.IdleInTransaction)
}

// TestValidSort verifies only supported sort identifiers pass, excluding empty values and SQL fragments.
func TestValidSort(t *testing.T) {
	t.Parallel()
	assert.True(t, ValidSort("activation-desc"))
	assert.True(t, ValidSort("kind-type"))
	assert.False(t, ValidSort("activation-desc; DROP TABLE schema_registry.releases"))
	assert.False(t, ValidSort(""))
}

// TestFilterValuesRejectsUnsafeInput verifies valid opaque filters pass while empty, control-containing, and oversized values fail.
func TestFilterValuesRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"quoted'value", "comment--value", "percent%value", "Unicode-\u00e4", "SELECT", "wildcard*value",
	} {
		assert.NoError(t, FilterValues(value), value)
	}
	assert.Error(t, FilterValues(""))
	assert.Error(t, FilterValues("line\nbreak"))
	assert.Error(t, FilterValues(string(make([]byte, 8193))))
}

// TestValidateReleaseChain verifies contiguous activations and matching predecessor coordinates distinguish a valid chain from inconsistent state.
func TestValidateReleaseChain(t *testing.T) {
	t.Parallel()
	sequenceTwo := int64(2)
	sequenceOne := int64(1)
	previousVersion := "1.0.0"
	previousDigest := "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	releases := []struct {
		version  string
		digest   string
		sequence int64
		previous *string
	}{
		{"1.1.0", "sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", sequenceTwo, &previousVersion},
		{"1.0.0", previousDigest, sequenceOne, nil},
	}
	actual := make([]releaseRow, 0, len(releases))
	for _, release := range releases {
		actual = append(actual, releaseRow{
			sequence: release.sequence,
			release: model.Release{
				SchemaVersion: release.version, ManifestDigest: release.digest, PreviousSchemaVersion: release.previous,
			},
		})
	}
	actual[0].release.PreviousManifestDigest = &previousDigest
	require.NoError(t, validateReleaseChain(actual))

	actual[0].release.PreviousManifestDigest = nil
	assert.ErrorIs(t, validateReleaseChain(actual), ErrInconsistentState)
}

// TestManifestChangesUsesAuthoritativeManifestBytes verifies exact stored change JSON wins, absent changes return nil, and malformed manifests fail.
func TestManifestChangesUsesAuthoritativeManifestBytes(t *testing.T) {
	t.Parallel()
	changes, err := manifestChanges([]byte(`{"schemaVersion":"1.0.0","changes":{"compatibility":"initial","artifacts":[]}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"compatibility":"initial","artifacts":[]}`, string(changes))

	changes, err = manifestChanges([]byte(`{"schemaVersion":"1.0.0"}`))
	require.NoError(t, err)
	assert.Nil(t, changes)

	_, err = manifestChanges([]byte(`not-json`))
	assert.ErrorIs(t, err, ErrInconsistentState)
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
