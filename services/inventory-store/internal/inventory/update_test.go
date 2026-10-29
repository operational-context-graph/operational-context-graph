// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// assetWithLUAS builds a minimal valid current Asset whose last_updated_at_source is luas (may be
// nil), for exercising Update.IsRedeliveryOf without a database.
func assetWithLUAS(t *testing.T, luas *time.Time) *Asset {
	t.Helper()
	a, err := ReconstituteAsset(ReconstituteAssetParams{
		ID:                  uuid.New(),
		SourceSystem:        "cmdb",
		SourceID:            "x",
		ArtifactType:        "server",
		CreatedAt:           time.Unix(0, 0).UTC(),
		LastUpdatedAtSource: luas,
		ValidFrom:           time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("reconstitute: %v", err)
	}
	return a
}

func TestUpdate_IsRedeliveryOf(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t0copy := t0 // equal instant, different pointer
	t1 := t0.Add(time.Hour)

	tests := []struct {
		name    string
		current *Asset
		update  Update
		want    bool
	}{
		{
			name:    "no current version is never a re-delivery",
			current: nil,
			update:  Update{LastUpdatedAtSource: &t0},
			want:    false,
		},
		{
			name:    "equal source update times is a re-delivery",
			current: assetWithLUAS(t, &t0),
			update:  Update{LastUpdatedAtSource: &t0copy},
			want:    true,
		},
		{
			name:    "advancing source update time is not a re-delivery",
			current: assetWithLUAS(t, &t0),
			update:  Update{LastUpdatedAtSource: &t1},
			want:    false,
		},
		{
			name:    "nil update timestamp cannot prove unchanged",
			current: assetWithLUAS(t, &t0),
			update:  Update{LastUpdatedAtSource: nil},
			want:    false,
		},
		{
			name:    "nil current timestamp cannot prove unchanged",
			current: assetWithLUAS(t, nil),
			update:  Update{LastUpdatedAtSource: &t0},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.update.IsRedeliveryOf(tt.current); got != tt.want {
				t.Errorf("IsRedeliveryOf = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdate_ValidityBoundary(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := (Update{LastUpdatedAtSource: &ts}).ValidityBoundary(); got == nil || !got.Equal(ts) {
		t.Errorf("ValidityBoundary with timestamp = %v, want %v", got, ts)
	}
	if got := (Update{LastUpdatedAtSource: nil}).ValidityBoundary(); got != nil {
		t.Errorf("ValidityBoundary without timestamp = %v, want nil", got)
	}
}
