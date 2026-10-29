// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package inventory

import "time"

// Update carries the source-reported fields of an incoming asset delivery. It is the write-side
// counterpart to ReconstituteAssetParams (which is read-side): it holds what a source sent, not a
// persisted row, so it has no id, created_at, or validity window.
type Update struct {
	Key                 NaturalKey
	IRI                 *string
	Name                *string
	ArtifactType        string
	Extensions          []byte
	IngestedAt          *time.Time
	CreatedAtSource     *time.Time
	LastUpdatedAtSource *time.Time
}

// IsRedeliveryOf reports whether this update is a re-delivery of the given current version, i.e.
// the same source change already stored. This is the temporal-dedup decision the legacy store
// made inline in SQL; here it is one domain rule, testable without a database.
//
// A re-delivery is when both sides carry a source update time and those times are equal. If either
// side lacks last_updated_at_source we cannot prove it is unchanged, so it is treated as a new
// version (matching the legacy behaviour).
func (u Update) IsRedeliveryOf(current *Asset) bool {
	if current == nil {
		return false
	}
	if u.LastUpdatedAtSource == nil || current.lastUpdatedAtSource == nil {
		return false
	}
	return current.lastUpdatedAtSource.Equal(*u.LastUpdatedAtSource)
}

// ValidityBoundary is the timestamp used both to close the superseded version's window and to open
// the new version's window, keeping the two contiguous. It is the source update time when known,
// so the temporal boundary reflects when the change happened at the source; otherwise nil, and the
// caller substitutes the database clock (NOW()).
func (u Update) ValidityBoundary() *time.Time {
	return u.LastUpdatedAtSource
}

// UpsertResult reports the outcome of applying one Update through the repository write path.
type UpsertResult struct {
	Asset      *Asset // the current version after applying the update (never nil on success)
	Created    bool   // true when this was a brand-new asset (no prior version existed)
	Redelivery bool   // true when nothing was written (unchanged re-delivery of the stored version)
}
