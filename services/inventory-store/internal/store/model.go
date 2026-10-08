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
	ID          uuid.UUID       `json:"id"`
	AssetType   string          `json:"asset_type"`
	Source      string          `json:"source"`
	ExternalID  string          `json:"external_id"`
	Name        *string         `json:"name,omitempty"`
	IPAddresses json.RawMessage `json:"ip_addresses,omitempty"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
	IngestedAt  time.Time       `json:"ingested_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// AssetInput is the request body for POST /assets.
type AssetInput struct {
	AssetType   string          `json:"asset_type"`
	Source      string          `json:"source"`
	ExternalID  string          `json:"external_id"`
	Name        *string         `json:"name,omitempty"`
	IPAddresses json.RawMessage `json:"ip_addresses,omitempty"`
	Attributes  json.RawMessage `json:"attributes,omitempty"`
}

// BatchResult is one outcome item returned from UpsertAssetBatch.
type BatchResult struct {
	ID      uuid.UUID `json:"id"`
	Outcome string    `json:"outcome"` // "created" or "updated"
}

// Relationship mirrors the relationships table.
type Relationship struct {
	ID               uuid.UUID       `json:"id"`
	SourceAssetID    uuid.UUID       `json:"source_asset_id"`
	TargetAssetID    uuid.UUID       `json:"target_asset_id"`
	RelationshipType string          `json:"relationship_type"`
	Source           string          `json:"source"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
	IngestedAt       time.Time       `json:"ingested_at"`
}

// RelationshipInput is the request body for POST /relationships.
type RelationshipInput struct {
	SourceAssetID    uuid.UUID       `json:"source_asset_id"`
	TargetAssetID    uuid.UUID       `json:"target_asset_id"`
	RelationshipType string          `json:"relationship_type"`
	Source           string          `json:"source"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
}

// Cursor encodes the keyset pagination position.
type Cursor struct {
	IngestedAt time.Time `json:"t"`
	ID         uuid.UUID `json:"i"`
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
