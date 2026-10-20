// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package artifact implements read-only artifact-discovery use cases.
package artifact

import (
	"context"
	"fmt"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// Service reads immutable artifacts through request-scoped snapshots.
type Service struct{ reader release.Reader }

// New creates an artifact-discovery service.
func New(reader release.Reader) *Service { return &Service{reader: reader} }

// Result preserves current and selected release metadata with a read result.
type Result[T any] struct {
	Current  model.CurrentState
	Selected string
	Value    T
}

// Descriptor is an artifact with its immutable resources and dependencies.
type Descriptor struct {
	Artifact           model.Artifact
	Resources          []model.Resource
	Dependencies       []model.Dependency
	SourceArtifactType string
}

// Page is one release-selected artifact page.
type Page struct {
	Current model.CurrentState
	Items   []Descriptor
}

func (s *Service) read(ctx context.Context, selected string, fn func(context.Context, release.Snapshot) error) (model.CurrentState, error) {
	var current model.CurrentState
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot release.Snapshot) error {
		if _, err := snapshot.CurrentRelease(readCtx); err != nil {
			return requestError(snapshot.CurrentState(), "", err)
		}
		current = snapshot.CurrentState()
		if err := fn(readCtx, snapshot); err != nil {
			return requestError(current, selected, err)
		}
		return nil
	})
	return current, err
}

func requestError(current model.CurrentState, selected string, err error) error {
	return release.WithRequestMetadata(current, selected, err)
}

// GetReleaseArtifact gets one release-selected immutable revision.
func (s *Service) GetReleaseArtifact(ctx context.Context, version, kind, artifactType string) (Result[model.Artifact], error) {
	var result Result[model.Artifact]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.ReleaseArtifact(readCtx, version, kind, artifactType)
		if err != nil {
			return err
		}
		result.Value = value
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// ReleaseDescriptor gets complete metadata scoped to one selected release.
func (s *Service) ReleaseDescriptor(ctx context.Context, version, kind, artifactType string) (Result[Descriptor], error) {
	var result Result[Descriptor]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.ReleaseArtifact(readCtx, version, kind, artifactType)
		if err != nil {
			return err
		}
		resources, err := snapshot.Resources(readCtx, value.Revision)
		if err != nil {
			return err
		}
		dependencies, err := snapshot.Dependencies(readCtx, value.Revision)
		if err != nil {
			return err
		}
		descriptor := Descriptor{Artifact: value, Resources: resources, Dependencies: dependencies}
		if value.Kind == "source-mapping" {
			detail, err := snapshot.SourceMappingByRevision(readCtx, version, value.Revision)
			if err != nil {
				return err
			}
			descriptor.SourceArtifactType = detail.SourceArtifactType
		}
		result.Value = descriptor
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// ImmutableDescriptor gets complete metadata without release-dependent links.
func (s *Service) ImmutableDescriptor(ctx context.Context, revision string) (Result[Descriptor], error) {
	var result Result[Descriptor]
	current, err := s.read(ctx, "", func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.Artifact(readCtx, revision)
		if err != nil {
			return err
		}
		resources, err := snapshot.Resources(readCtx, value.Revision)
		if err != nil {
			return err
		}
		dependencies, err := snapshot.Dependencies(readCtx, value.Revision)
		if err != nil {
			return err
		}
		result.Value = Descriptor{Artifact: value, Resources: resources, Dependencies: dependencies}
		return nil
	})
	result.Current = current
	return result, err
}

// List returns a filtered, bounded release-selected artifact page.
func (s *Service) List(ctx context.Context, version string, filter model.ArtifactFilter, afterKind, afterType *string, limit int) (Page, error) {
	var result Page
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		items, err := snapshot.ReleaseArtifacts(readCtx, version, filter, afterKind, afterType, limit)
		if err != nil {
			return err
		}
		result.Items = make([]Descriptor, 0, len(items))
		for _, value := range items {
			resources, err := snapshot.Resources(readCtx, value.Revision)
			if err != nil {
				return err
			}
			dependencies, err := snapshot.Dependencies(readCtx, value.Revision)
			if err != nil {
				return err
			}
			descriptor := Descriptor{Artifact: value, Resources: resources, Dependencies: dependencies}
			if value.Kind == "source-mapping" {
				detail, err := snapshot.SourceMappingByRevision(readCtx, version, value.Revision)
				if err != nil {
					return err
				}
				descriptor.SourceArtifactType = detail.SourceArtifactType
			}
			result.Items = append(result.Items, descriptor)
		}
		return nil
	})
	result.Current = current
	return result, err
}

// History returns activated logical artifact selections in descending sequence.
func (s *Service) History(ctx context.Context, kind, artifactType string, before, snapshotSequence *int64, limit int) (Result[[]model.ReleaseArtifact], error) {
	var result Result[[]model.ReleaseArtifact]
	current, err := s.read(ctx, "", func(readCtx context.Context, snapshot release.Snapshot) error {
		maximum := snapshot.CurrentState().SnapshotSequence
		if snapshotSequence != nil {
			maximum = *snapshotSequence
		}
		items, err := snapshot.ArtifactHistory(readCtx, kind, artifactType, before, maximum, limit)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return postgres.ErrNotFound
		}
		result.Current = snapshot.CurrentState()
		result.Current.SnapshotSequence = maximum
		result.Value = items
		return nil
	})
	if result.Current.SchemaVersion == "" {
		result.Current = current
	}
	return result, err
}

// GetArtifact gets an activated-reachable immutable revision.
func (s *Service) GetArtifact(ctx context.Context, revision string) (Result[model.Artifact], error) {
	var result Result[model.Artifact]
	current, err := s.read(ctx, "", func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.Artifact(readCtx, revision)
		if err != nil {
			return err
		}
		result.Value = value
		return nil
	})
	result.Current = current
	return result, err
}

// GetResource gets a named exact resource after activated reachability checks.
func (s *Service) GetResource(ctx context.Context, revision, name string) (Result[model.Resource], error) {
	var result Result[model.Resource]
	current, err := s.read(ctx, "", func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.Resource(readCtx, revision, name)
		if err != nil {
			return err
		}
		result.Value = value
		return nil
	})
	result.Current = current
	return result, err
}

// ResolveURI resolves one exact release-scoped URI entry.
func (s *Service) ResolveURI(ctx context.Context, version, uri string) (Result[model.URIEntry], error) {
	var result Result[model.URIEntry]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		value, err := snapshot.URIEntry(readCtx, version, uri)
		if err != nil {
			return err
		}
		result.Value = value
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// ValidateCanonical verifies that all immutable canonical-path coordinates match.
func ValidateCanonical(value model.Artifact, kind, artifactType, revision string) error {
	if value.Kind != kind || value.Type != artifactType || value.Revision != revision {
		return postgres.ErrNotFound
	}
	return nil
}

// EnsureResourceOwner prevents a resource from being served under another revision.
func EnsureResourceOwner(value model.Resource, revision string) error {
	if value.ArtifactRevision != revision {
		return fmt.Errorf("resource owner: %w", postgres.ErrInconsistentState)
	}
	return nil
}
