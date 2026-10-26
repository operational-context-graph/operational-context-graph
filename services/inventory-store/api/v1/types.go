// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package v1

import (
	"encoding/json"

	"github.com/google/uuid"
)

// UpsertAssetRequest is the body for POST /v1/assets.
type UpsertAssetRequest struct {
	IRI                 string          `json:"iri"                    validate:"required"`
	Name                string          `json:"name,omitempty"`
	ArtifactType        string          `json:"artifact_type"          validate:"required"`
	SourceSystem        string          `json:"source_system"          validate:"required"`
	SourceID            string          `json:"source_id"              validate:"required"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
}

// AssetResponse is the body returned for a single asset.
type AssetResponse struct {
	ID                  uuid.UUID       `json:"id"`
	IRI                 string          `json:"iri"`
	Name                *string         `json:"name,omitempty"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceID            string          `json:"source_id"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAt           string          `json:"created_at"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
	ValidFrom           string          `json:"valid_from"`
	ValidTo             *string         `json:"valid_to,omitempty"`
}

// UpsertAssetResponse wraps AssetResponse with a created flag.
type UpsertAssetResponse struct {
	Asset   AssetResponse `json:"asset"`
	Created bool          `json:"created"`
}

// AssetListResponse is the body returned for GET /v1/assets (list).
type AssetListResponse struct {
	Items      []AssetResponse `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// BatchUpsertAssetRequest is the body for POST /v1/assets/batch.
type BatchUpsertAssetRequest struct {
	Assets []UpsertAssetRequest `json:"assets" validate:"required,min=1"`
}

// BatchResult is one item in the batch response.
type BatchResult struct {
	ID      uuid.UUID `json:"id"`
	Outcome string    `json:"outcome"`
}

// UpsertRelationshipRequest is the body for POST /v1/relationships.
// Callers MUST provide either source_asset_id/target_asset_id (UUIDs) or
// source_key/target_key ([source_system, source_id] arrays). When keys are
// provided the store resolves asset UUIDs internally.
type UpsertRelationshipRequest struct {
	SourceAssetID       *uuid.UUID      `json:"source_asset_id,omitempty"`
	TargetAssetID       *uuid.UUID      `json:"target_asset_id,omitempty"`
	ArtifactType        string          `json:"artifact_type"          validate:"required"`
	SourceSystem        string          `json:"source_system"          validate:"required"`
	SourceKey           []string        `json:"source_key,omitempty"`
	TargetKey           []string        `json:"target_key,omitempty"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
}

// RelationshipResponse is the body returned for a single relationship.
type RelationshipResponse struct {
	ID                  uuid.UUID       `json:"id"`
	SourceAssetID       uuid.UUID       `json:"source_asset_id"`
	TargetAssetID       uuid.UUID       `json:"target_asset_id"`
	ArtifactType        string          `json:"artifact_type"`
	SourceSystem        string          `json:"source_system"`
	SourceKey           string          `json:"source_key"`
	TargetKey           string          `json:"target_key"`
	Extensions          json.RawMessage `json:"extensions,omitempty"`
	IngestedAt          *string         `json:"ingested_at,omitempty"`
	CreatedAt           string          `json:"created_at"`
	CreatedAtSource     *string         `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string         `json:"last_updated_at_source,omitempty"`
	ValidFrom           string          `json:"valid_from"`
	ValidTo             *string         `json:"valid_to,omitempty"`
}

// UpsertRelationshipResponse wraps RelationshipResponse with a created flag.
type UpsertRelationshipResponse struct {
	Relationship RelationshipResponse `json:"relationship"`
	Created      bool                 `json:"created"`
}

// RelationshipListResponse is the body for GET /v1/assets/:id/relationships.
type RelationshipListResponse struct {
	Items []RelationshipResponse `json:"items"`
}

// BatchUpsertRelationshipRequest is the body for POST /v1/relationships/batch.
type BatchUpsertRelationshipRequest struct {
	Relationships []UpsertRelationshipRequest `json:"relationships" validate:"required,min=1"`
}
