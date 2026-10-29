// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"context"
	"log/slog"
	"reflect"

	"github.com/google/uuid"

	"github.com/operational-context-graph/inventory-store/internal/inventory"
	"github.com/operational-context-graph/inventory-store/internal/store"
)

// ReadPath selects which implementation serves asset reads.
type ReadPath string

const (
	// ReadPathDAO uses the legacy *store.Store directly. Default; production-safe.
	ReadPathDAO ReadPath = "dao"
	// ReadPathRepo serves reads from the new PostgresAssetRepository.
	ReadPathRepo ReadPath = "repo"
	// ReadPathCompare serves the legacy result but also runs the repository and logs any
	// divergence. Reads only, so running both is side-effect free.
	ReadPathCompare ReadPath = "compare"
)

// ParseReadPath normalises an env/config string to a ReadPath, defaulting to DAO.
func ParseReadPath(s string) ReadPath {
	switch ReadPath(s) {
	case ReadPathRepo:
		return ReadPathRepo
	case ReadPathCompare:
		return ReadPathCompare
	default:
		return ReadPathDAO
	}
}

// StoreReader decorates *store.Store so asset reads can be served by the new repository
// without changing any existing code. It embeds the concrete store, so every method of the
// httpapi db interface (writes, relationships, health) passes straight through; only
// GetAsset and ListAssets are overridden and routed per ReadPath.
//
// This is the whole "switch": main.go constructs this instead of a bare *store.Store when the
// read-path flag is set, and hands it to the unchanged httpapi.NewServer.
type StoreReader struct {
	*store.Store // legacy path; satisfies the full db interface

	repo *PostgresAssetRepository
	path ReadPath
}

// NewStoreReader wraps a store and repository behind the chosen read path.
func NewStoreReader(s *store.Store, repo *PostgresAssetRepository, path ReadPath) *StoreReader {
	return &StoreReader{Store: s, repo: repo, path: path}
}

// GetAsset serves a single asset per the configured read path.
func (r *StoreReader) GetAsset(ctx context.Context, id uuid.UUID) (store.Asset, error) {
	switch r.path {
	case ReadPathRepo:
		a, err := r.repo.GetAsset(ctx, id)
		if err != nil {
			return store.Asset{}, err
		}
		return domainToStoreAsset(a), nil
	case ReadPathCompare:
		legacy, legacyErr := r.Store.GetAsset(ctx, id)
		shadow, shadowErr := r.repo.GetAsset(ctx, id)
		var shadowAsset store.Asset
		if shadowErr == nil {
			shadowAsset = domainToStoreAsset(shadow)
		}
		logMismatch("GetAsset",
			(legacyErr == nil) == (shadowErr == nil) && reflect.DeepEqual(legacy, shadowAsset),
			slog.String("asset_id", id.String()),
			slog.Bool("legacy_err", legacyErr != nil),
			slog.Bool("shadow_err", shadowErr != nil),
		)
		return legacy, legacyErr
	default:
		return r.Store.GetAsset(ctx, id)
	}
}

// ListAssets serves a page of assets per the configured read path.
func (r *StoreReader) ListAssets(ctx context.Context, artifactType, sourceSystem, sourceID string, limit int, after *store.Cursor) ([]store.Asset, *store.Cursor, error) {
	switch r.path {
	case ReadPathRepo:
		ds, next, err := r.repo.ListAssets(ctx, artifactType, sourceSystem, sourceID, limit, after)
		if err != nil {
			return nil, nil, err
		}
		return domainsToStoreAssets(ds), next, nil
	case ReadPathCompare:
		legacy, legacyNext, legacyErr := r.Store.ListAssets(ctx, artifactType, sourceSystem, sourceID, limit, after)
		shadow, shadowNext, shadowErr := r.repo.ListAssets(ctx, artifactType, sourceSystem, sourceID, limit, after)
		match := (legacyErr == nil) == (shadowErr == nil) &&
			reflect.DeepEqual(legacy, domainsToStoreAssets(shadow)) &&
			reflect.DeepEqual(legacyNext, shadowNext)
		logMismatch("ListAssets", match,
			slog.Int("legacy_count", len(legacy)),
			slog.Int("shadow_count", len(shadow)),
			slog.Bool("legacy_err", legacyErr != nil),
			slog.Bool("shadow_err", shadowErr != nil),
		)
		return legacy, legacyNext, legacyErr
	default:
		return r.Store.ListAssets(ctx, artifactType, sourceSystem, sourceID, limit, after)
	}
}

func logMismatch(op string, match bool, attrs ...slog.Attr) {
	if match {
		return
	}
	args := make([]any, 0, len(attrs)+1)
	args = append(args, slog.String("op", op))
	for _, a := range attrs {
		args = append(args, a)
	}
	slog.Warn("read-path compare mismatch", args...)
}

// domainToStoreAsset converts a domain Asset back to the legacy store.Asset shape, so the
// repository path is transparent to the unchanged httpapi db interface.
func domainToStoreAsset(a *inventory.Asset) store.Asset {
	v := a.Validity()
	return store.Asset{
		ID:                  a.ID(),
		IRI:                 a.IRI(),
		Name:                a.Name(),
		ArtifactType:        a.ArtifactType(),
		SourceSystem:        a.Key().SourceSystem,
		SourceID:            a.Key().SourceID,
		Extensions:          a.Extensions(),
		IngestedAt:          a.IngestedAt(),
		CreatedAt:           a.CreatedAt(),
		CreatedAtSource:     a.CreatedAtSource(),
		LastUpdatedAtSource: a.LastUpdatedAtSource(),
		ValidFrom:           v.From,
		ValidTo:             v.To,
	}
}

func domainsToStoreAssets(ds []*inventory.Asset) []store.Asset {
	if ds == nil {
		return nil
	}
	out := make([]store.Asset, len(ds))
	for i, d := range ds {
		out[i] = domainToStoreAsset(d)
	}
	return out
}
