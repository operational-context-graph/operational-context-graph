// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/operational-context-graph/inventory-store/internal/apperr"
)

// ReconstituteAssetParams carries the raw values read from storage into the factory.
// Pointers mirror nullable columns; the factory validates and assembles a domain Asset.
type ReconstituteAssetParams struct {
	ID                  uuid.UUID
	SourceSystem        string
	SourceID            string
	IRI                 *string
	Name                *string
	ArtifactType        string
	Extensions          json.RawMessage
	IngestedAt          *time.Time
	CreatedAt           time.Time
	CreatedAtSource     *time.Time
	LastUpdatedAtSource *time.Time
	ValidFrom           time.Time
	ValidTo             *time.Time
}

// ReconstituteAsset rebuilds a domain Asset from persisted values, enforcing the invariants
// that the schema guarantees. It is the only way to obtain an Asset from outside the package,
// so an Asset in memory is always well-formed.
//
// This is the reconstitution factory (cf. Wild Workouts' UnmarshalHourFromDatabase): it does
// not generate new identity or timestamps; it validates data that already exists.
func ReconstituteAsset(p ReconstituteAssetParams) (*Asset, error) {
	if p.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: asset id is required", apperr.ErrInvalidInput)
	}
	key := NaturalKey{SourceSystem: p.SourceSystem, SourceID: p.SourceID}
	if key.IsZero() {
		return nil, fmt.Errorf("%w: natural key (source_system, source_id) is required", apperr.ErrInvalidInput)
	}
	if p.ArtifactType == "" {
		return nil, fmt.Errorf("%w: artifact_type is required", apperr.ErrInvalidInput)
	}
	if p.ValidFrom.IsZero() {
		return nil, fmt.Errorf("%w: valid_from is required", apperr.ErrInvalidInput)
	}
	// Temporal invariant: a closed version must not end before it began.
	if p.ValidTo != nil && p.ValidTo.Before(p.ValidFrom) {
		return nil, fmt.Errorf("%w: valid_to %s precedes valid_from %s",
			apperr.ErrInvalidInput, p.ValidTo.UTC().Format(time.RFC3339), p.ValidFrom.UTC().Format(time.RFC3339))
	}

	ext := p.Extensions
	if len(ext) == 0 {
		ext = json.RawMessage("{}")
	}

	return &Asset{
		id:                  p.ID,
		key:                 key,
		iri:                 p.IRI,
		name:                p.Name,
		artifact:            p.ArtifactType,
		extensions:          ext,
		ingestedAt:          p.IngestedAt,
		createdAt:           p.CreatedAt,
		createdAtSource:     p.CreatedAtSource,
		lastUpdatedAtSource: p.LastUpdatedAtSource,
		validity:            Validity{From: p.ValidFrom, To: p.ValidTo},
	}, nil
}
