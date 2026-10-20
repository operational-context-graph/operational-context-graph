// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package release implements read-only release-discovery use cases.
package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

var ErrVersionsNotComparable = errors.New("versions are not comparable")

// RequestError preserves request-snapshot metadata when an operation fails
// after the current release pointer resolves.
type RequestError struct {
	Current               model.CurrentState
	SelectedSchemaVersion string
	err                   error
}

func (e *RequestError) Error() string { return e.err.Error() }
func (e *RequestError) Unwrap() error { return e.err }

// RequestMetadata returns resolved version metadata from an operation error.
func RequestMetadata(err error) (model.CurrentState, string, bool) {
	var requestError *RequestError
	if !errors.As(err, &requestError) {
		return model.CurrentState{}, "", false
	}
	return requestError.Current, requestError.SelectedSchemaVersion, true
}

func withRequestMetadata(current model.CurrentState, selected string, err error) error {
	if err == nil || current.SchemaVersion == "" {
		return err
	}
	return &RequestError{Current: current, SelectedSchemaVersion: selected, err: err}
}

// WithRequestMetadata attaches safely resolved request versions to an error.
func WithRequestMetadata(current model.CurrentState, selected string, err error) error {
	return withRequestMetadata(current, selected, err)
}

// Snapshot is the narrow read-only release storage contract.
type Snapshot interface {
	CurrentState() model.CurrentState
	CurrentRelease(context.Context) (model.Release, error)
	Release(context.Context, string) (model.Release, error)
	ReleasesAt(context.Context, *int64, int64, int) ([]model.Release, error)
	IsAncestor(context.Context, string, string) (bool, error)
	Artifact(context.Context, string) (model.Artifact, error)
	ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error)
	ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error)
	ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error)
	Resources(context.Context, string) ([]model.Resource, error)
	Resource(context.Context, string, string) (model.Resource, error)
	Dependencies(context.Context, string) ([]model.Dependency, error)
	URIEntry(context.Context, string, string) (model.URIEntry, error)
	SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error)
}

// Reader provides a repeatable-read snapshot. It has no mutation methods.
type Reader interface {
	Read(context.Context, func(context.Context, Snapshot) error) error
}

// Service exposes release discovery without transport dependencies.
type Service struct{ reader Reader }

func New(reader Reader) *Service { return &Service{reader: reader} }

type Discovery struct{ Current model.Release }

func (s *Service) Discovery(ctx context.Context) (Discovery, error) {
	var result Discovery
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		release, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		result.Current = release
		return nil
	})
	return result, err
}

type ReleasePage struct {
	Current model.CurrentState
	Items   []model.Release
}

// List returns one immutable activation-order page. The caller supplies the
// cursor-bound snapshot ceiling rather than trusting a later current pointer.
func (s *Service) List(ctx context.Context, before *int64, snapshotSequence *int64, limit int) (ReleasePage, error) {
	var result ReleasePage
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		state := snapshot.CurrentState()
		ceiling := state.SnapshotSequence
		if snapshotSequence != nil {
			ceiling = *snapshotSequence
		}
		items, err := snapshot.ReleasesAt(readCtx, before, ceiling, limit)
		if err != nil {
			return withRequestMetadata(state, "", err)
		}
		state.SnapshotSequence = ceiling
		result.Current = state
		result.Items = items
		return nil
	})
	return result, err
}

type Descriptor struct {
	Current model.CurrentState
	Release model.Release
}

func (s *Service) Get(ctx context.Context, version string) (Descriptor, error) {
	var result Descriptor
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		state := snapshot.CurrentState()
		release, err := snapshot.Release(readCtx, version)
		if err != nil {
			return withRequestMetadata(state, "", err)
		}
		if parsed, parseErr := manifest.Parse(release.ManifestBytes); parseErr != nil || parsed.SchemaVersion != release.SchemaVersion {
			return withRequestMetadata(state, version, fmt.Errorf("validate stored release manifest: %w", postgres.ErrInconsistentState))
		}
		result = Descriptor{Current: state, Release: release}
		return nil
	})
	return result, err
}

// Compare derives a net change summary from two endpoint manifests. It never
// concatenates intermediate summaries.
func (s *Service) Compare(ctx context.Context, from, to string) (manifest.Changes, model.CurrentState, error) {
	var changes manifest.Changes
	var current model.CurrentState
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		current = snapshot.CurrentState()
		before, err := snapshot.Release(readCtx, from)
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		after, err := snapshot.Release(readCtx, to)
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		if before.SchemaVersion == after.SchemaVersion || before.ActivationSequence >= after.ActivationSequence {
			return withRequestMetadata(current, "", ErrVersionsNotComparable)
		}
		isAncestor, err := snapshot.IsAncestor(readCtx, before.SchemaVersion, after.SchemaVersion)
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		if !isAncestor {
			return withRequestMetadata(current, "", ErrVersionsNotComparable)
		}
		beforeManifest, err := manifest.Parse(before.ManifestBytes)
		if err != nil {
			return withRequestMetadata(current, "", fmt.Errorf("parse stored source manifest: %w", postgres.ErrInconsistentState))
		}
		afterManifest, err := manifest.Parse(after.ManifestBytes)
		if err != nil {
			return withRequestMetadata(current, "", fmt.Errorf("parse stored target manifest: %w", postgres.ErrInconsistentState))
		}
		artifacts := make(map[manifest.Key]artifact.Artifact)
		for _, entry := range append(append([]manifest.Artifact{}, beforeManifest.Artifacts...), afterManifest.Artifacts...) {
			stored, err := snapshot.Artifact(readCtx, entry.Revision)
			if err != nil {
				return withRequestMetadata(current, "", err)
			}
			artifacts[manifest.Key{Kind: entry.Kind, Type: entry.Type, Revision: entry.Revision}] = artifact.Artifact{
				Kind: entry.Kind, Type: entry.Type, Revision: entry.Revision, Payload: artifact.Descriptor{MediaType: stored.Payload.MediaType, Digest: stored.Payload.Digest, Size: stored.Payload.Size}, PayloadBytes: stored.Payload.Bytes,
			}
		}
		changes, err = manifest.Diff(beforeManifest, afterManifest, manifest.Context{Artifacts: artifacts})
		return withRequestMetadata(current, "", err)
	})
	return changes, current, err
}
