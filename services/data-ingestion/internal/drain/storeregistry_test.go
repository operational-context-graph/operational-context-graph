package drain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

func validLimits() store.BatchLimits {
	return store.BatchLimits{MaxRecords: 10, MaxBytes: 1 << 20, MaxLinger: time.Second}
}

func TestRegistry_Register_Valid(t *testing.T) {
	t.Parallel()
	reg := NewStoreRegistry()
	w := &fakeWriter{id: "telemetry", limits: validLimits()}

	require.NoError(t, reg.Register(w))
	got, ok := reg.Writer("telemetry")
	assert.True(t, ok)
	assert.Same(t, w, got)
	assert.Equal(t, 1, reg.Len())
}

func TestRegistry_Register_DuplicateRejected(t *testing.T) {
	t.Parallel()
	reg := NewStoreRegistry()
	require.NoError(t, reg.Register(&fakeWriter{id: "telemetry", limits: validLimits()}))

	err := reg.Register(&fakeWriter{id: "telemetry", limits: validLimits()})
	require.Error(t, err)
	assert.Equal(t, 1, reg.Len())
}

func TestRegistry_Register_InvalidLimitsRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limits store.BatchLimits
	}{
		{"zero records", store.BatchLimits{MaxRecords: 0, MaxLinger: time.Second}},
		{"zero linger", store.BatchLimits{MaxRecords: 10, MaxLinger: 0}},
		{"negative bytes", store.BatchLimits{MaxRecords: 10, MaxBytes: -1, MaxLinger: time.Second}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := NewStoreRegistry()
			err := reg.Register(&fakeWriter{id: "telemetry", limits: tc.limits})
			require.Error(t, err)
			assert.ErrorIs(t, err, store.ErrInvalidLimits)
			assert.Equal(t, 0, reg.Len())
		})
	}
}
