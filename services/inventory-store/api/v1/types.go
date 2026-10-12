// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package v1

import "github.com/google/uuid"

// UpsertAssetRequest is the body for POST /v1/assets.
type UpsertAssetRequest struct {
	ArtifactType        string  `json:"artifact_type"          validate:"required"`
	SourceSystem        string  `json:"source_system"          validate:"required"`
	SourceID            string  `json:"source_id"              validate:"required"`
	Extensions          []byte  `json:"extensions,omitempty"`
	CreatedAtSource     *string `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string `json:"last_updated_at_source,omitempty"`
}

// AssetResponse is the body returned for a single asset.
type AssetResponse struct {
	ID                  uuid.UUID `json:"id"`
	ArtifactType        string    `json:"artifact_type"`
	SourceSystem        string    `json:"source_system"`
	SourceID            string    `json:"source_id"`
	Extensions          []byte    `json:"extensions,omitempty"`
	CreatedAt           string    `json:"created_at"`
	CreatedAtSource     *string   `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string   `json:"last_updated_at_source,omitempty"`
	ValidFrom           string    `json:"valid_from"`
	ValidTo             *string   `json:"valid_to,omitempty"`
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
type UpsertRelationshipRequest struct {
	SourceAssetID       uuid.UUID `json:"source_asset_id"        validate:"required"`
	TargetAssetID       uuid.UUID `json:"target_asset_id"        validate:"required"`
	ArtifactType        string    `json:"artifact_type"          validate:"required"`
	SourceSystem        string    `json:"source_system"          validate:"required"`
	Extensions          []byte    `json:"extensions,omitempty"`
	CreatedAtSource     *string   `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string   `json:"last_updated_at_source,omitempty"`
}

// RelationshipResponse is the body returned for a single relationship.
type RelationshipResponse struct {
	ID                  uuid.UUID `json:"id"`
	SourceAssetID       uuid.UUID `json:"source_asset_id"`
	TargetAssetID       uuid.UUID `json:"target_asset_id"`
	ArtifactType        string    `json:"artifact_type"`
	SourceSystem        string    `json:"source_system"`
	Extensions          []byte    `json:"extensions,omitempty"`
	CreatedAt           string    `json:"created_at"`
	CreatedAtSource     *string   `json:"created_at_source,omitempty"`
	LastUpdatedAtSource *string   `json:"last_updated_at_source,omitempty"`
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
