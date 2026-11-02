package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchLimits_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		limits  BatchLimits
		wantErr bool
	}{
		{
			name:   "valid with byte trigger",
			limits: BatchLimits{MaxRecords: 100, MaxBytes: 1 << 20, MaxLinger: time.Second},
		},
		{
			name:   "valid with byte trigger disabled",
			limits: BatchLimits{MaxRecords: 1, MaxBytes: 0, MaxLinger: time.Millisecond},
		},
		{
			name:   "valid with record trigger disabled and byte trigger set",
			limits: BatchLimits{MaxRecords: 0, MaxBytes: 500 << 20, MaxLinger: time.Second},
		},
		{
			name:    "both record and byte triggers disabled rejected",
			limits:  BatchLimits{MaxRecords: 0, MaxBytes: 0, MaxLinger: time.Second},
			wantErr: true,
		},
		{
			name:    "negative record limit rejected",
			limits:  BatchLimits{MaxRecords: -1, MaxBytes: 0, MaxLinger: time.Second},
			wantErr: true,
		},
		{
			name:    "zero linger limit rejected",
			limits:  BatchLimits{MaxRecords: 10, MaxBytes: 0, MaxLinger: 0},
			wantErr: true,
		},
		{
			name:    "negative byte limit rejected",
			limits:  BatchLimits{MaxRecords: 10, MaxBytes: -1, MaxLinger: time.Second},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.limits.Validate()
			if tc.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidLimits)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPermanentError_Error(t *testing.T) {
	t.Parallel()
	err := &PermanentError{Reason: "schema rejected by store"}
	assert.Equal(t, "schema rejected by store", err.Error())
}
