// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package asset

import (
	"context"

	"github.com/google/uuid"
)

// AssetReader is the read port the inventory domain requires for assets. It is defined here, in
// the consumer, not in the adapter: any backend (Postgres, an in-memory fake, a cache) satisfies
// it structurally. GetAsset and ListAssets return the current (open) version only.
//
// Pagination uses the domain Cursor (see cursor.go), so the port has no dependency on the legacy
// store package.
type AssetReader interface {
	GetAsset(ctx context.Context, id uuid.UUID) (*Asset, error)
	ListAssets(ctx context.Context, artifactType, sourceSystem, sourceID string, limit int, after *Cursor) ([]*Asset, *Cursor, error)
}

// AssetWriter is the write port: it applies deliveries under the temporal-versioning contract.
type AssetWriter interface {
	Upsert(ctx context.Context, u Update) (*Asset, bool, error)
	UpsertBatch(ctx context.Context, updates []Update) ([]UpsertResult, error)
}

// AssetRepository is the full asset persistence port. Split into reader/writer so a consumer that
// needs only one half (e.g. the read-path switch, or an ingest worker) can depend on the narrow
// interface instead of the whole thing.
type AssetRepository interface {
	AssetReader
	AssetWriter
}
