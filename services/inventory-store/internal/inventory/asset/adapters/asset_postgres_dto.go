// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Package adapters holds repository implementations for the inventory domain.
// Each implementation maps between its backend's row representation and the
// DB-agnostic domain types in package asset.
package adapters

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/operational-context-graph/inventory-store/internal/inventory/asset"
)

// postgresAsset mirrors a row of the assets table. It is the persistence DTO:
// unexported, backend-specific, never returned to callers. (Cf. Wild Workouts' mysqlHour.)
type postgresAsset struct {
	ID                  uuid.UUID
	IRI                 *string
	Name                *string
	ArtifactType        string
	SourceSystem        string
	SourceID            string
	Extensions          json.RawMessage
	IngestedAt          *time.Time
	CreatedAt           time.Time
	CreatedAtSource     *time.Time
	LastUpdatedAtSource *time.Time
	ValidFrom           time.Time
	ValidTo             *time.Time
}

// toDomain maps a scanned row to a domain Asset, passing through the invariant-guarding factory.
func (r postgresAsset) toDomain() (*asset.Asset, error) {
	return asset.NewAssetFromStorage(asset.AssetFromStorageParams{
		ID:                  r.ID,
		SourceSystem:        r.SourceSystem,
		SourceID:            r.SourceID,
		IRI:                 r.IRI,
		Name:                r.Name,
		ArtifactType:        r.ArtifactType,
		Extensions:          r.Extensions,
		IngestedAt:          r.IngestedAt,
		CreatedAt:           r.CreatedAt,
		CreatedAtSource:     r.CreatedAtSource,
		LastUpdatedAtSource: r.LastUpdatedAtSource,
		ValidFrom:           r.ValidFrom,
		ValidTo:             r.ValidTo,
	})
}
