// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

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

func TestTimeoutsCapToRequestDeadline(t *testing.T) {
	t.Parallel()
	timeouts := Timeouts{Acquire: time.Second, Query: time.Second, Statement: 5 * time.Second, Lock: 3 * time.Second, IdleInTransaction: 2 * time.Second}
	capped := timeouts.capped(500 * time.Millisecond)
	assert.Equal(t, 500*time.Millisecond, capped.Statement)
	assert.Equal(t, 500*time.Millisecond, capped.Lock)
	assert.Equal(t, 500*time.Millisecond, capped.IdleInTransaction)
}

func TestProjectionCommitmentDeterministic(t *testing.T) {
	t.Parallel()
	first := ProjectionCommitmentHex([]string{"resource\titem", "artifact\titem"})
	second := ProjectionCommitmentHex([]string{"artifact\titem", "resource\titem"})
	assert.Equal(t, first, second)
	assert.NotEqual(t, first, ProjectionCommitmentHex([]string{"artifact\tchanged", "resource\titem"}))
}

func TestValidSort(t *testing.T) {
	t.Parallel()
	assert.True(t, ValidSort("activation-desc"))
	assert.True(t, ValidSort("kind-type"))
	assert.False(t, ValidSort("activation-desc; DROP TABLE schema_registry.releases"))
	assert.False(t, ValidSort(""))
}

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

func TestValidateReleaseChain(t *testing.T) {
	t.Parallel()
	sequenceTwo := int64(2)
	sequenceOne := int64(1)
	previousVersion := "1.0.0"
	previousDigest := "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	releases := []struct {
		version  string
		digest   string
		sequence *int64
		previous *string
	}{
		{"1.1.0", "sha-256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", &sequenceTwo, &previousVersion},
		{"1.0.0", previousDigest, &sequenceOne, nil},
	}
	actual := make([]model.Release, 0, len(releases))
	for _, release := range releases {
		actual = append(actual, model.Release{
			SchemaVersion: release.version, ManifestDigest: release.digest, ActivationSequence: release.sequence,
			PreviousSchemaVersion: release.previous,
		})
	}
	actual[0].PreviousManifestDigest = &previousDigest
	require.NoError(t, validateReleaseChain(actual))

	actual[0].PreviousManifestDigest = nil
	assert.ErrorIs(t, validateReleaseChain(actual), ErrInconsistentState)
}
