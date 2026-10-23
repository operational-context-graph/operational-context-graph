// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Asset mirrors the assets table.
type Asset struct {
	ID                  uuid.UUID       `json:"id"`
	IRI                 *string         `json:"iri,omitempty"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceID            string          `json:"source_id"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *time.Time      `json:"ingested_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	CreatedAtSource     *time.Time      `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *time.Time      `json:"last_updated_at_source,omitempty"`
	ValidFrom           time.Time       `json:"valid_from"`
	ValidTo             *time.Time      `json:"valid_to,omitempty"`
}

// AssetInput is the request body for POST /assets.
type AssetInput struct {
	IRI                 string          `json:"iri"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceID            string          `json:"source_id"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *time.Time      `json:"ingested_at,omitempty"`
	CreatedAtSource     *time.Time      `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *time.Time      `json:"last_updated_at_source,omitempty"`
}

// BatchResult is one outcome item returned from UpsertAssetBatch.
type BatchResult struct {
	ID      uuid.UUID `json:"id"`
	Outcome string    `json:"outcome"` // "created" or "updated"
}

// Relationship mirrors the relationships table.
type Relationship struct {
	ID                  uuid.UUID       `json:"id"`
	IRI                 *string         `json:"iri,omitempty"`
	SourceAssetID       uuid.UUID       `json:"source_asset_id"`
	TargetAssetID       uuid.UUID       `json:"target_asset_id"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceID            *string         `json:"source_id,omitempty"`
	SourceKey           *string         `json:"source_key,omitempty"`
	TargetKey           *string         `json:"target_key,omitempty"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *time.Time      `json:"ingested_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	CreatedAtSource     *time.Time      `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *time.Time      `json:"last_updated_at_source,omitempty"`
	ValidFrom           time.Time       `json:"valid_from"`
	ValidTo             *time.Time      `json:"valid_to,omitempty"`
}

// RelationshipInput is the request body for POST /relationships.
type RelationshipInput struct {
	IRI                 string          `json:"iri"`
	SourceAssetID       uuid.UUID       `json:"source_asset_id"`
	TargetAssetID       uuid.UUID       `json:"target_asset_id"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceID            string          `json:"source_id"`
	SourceKey           string          `json:"source_key"`
	TargetKey           string          `json:"target_key"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *time.Time      `json:"ingested_at,omitempty"`
	CreatedAtSource     *time.Time      `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *time.Time      `json:"last_updated_at_source,omitempty"`
}

// Cursor encodes the keyset pagination position.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	return c, json.Unmarshal(b, &c)
}
