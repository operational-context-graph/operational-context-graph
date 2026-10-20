// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package artifact implements read-only artifact-discovery use cases.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

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

// SchemaPackage contains the selected schema and its bound context documents.
// Documents remain raw JSON so boolean schemas preserve their exact JSON value.
type SchemaPackage struct {
	Schema   Descriptor
	Contexts []Descriptor
}

// MappingPackage contains one release-consistent mapping and its dependencies.
type MappingPackage struct {
	Mapping Descriptor
	Detail  model.SourceMappingDetail
	Program model.Resource
	Source  Descriptor
	Target  Descriptor
	Stores  []model.StorageEntry
}

// Mapping is an enriched, release-selected source mapping.
type Mapping struct {
	Descriptor Descriptor
	Detail     model.SourceMappingDetail
}

// SchemaStores resolves logical store assignments for one selected schema.
func (s *Service) SchemaStores(ctx context.Context, version, artifactType string) (Result[[]model.StorageEntry], error) {
	var result Result[[]model.StorageEntry]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		schema, err := snapshot.ReleaseArtifact(readCtx, version, "schema", artifactType)
		if err != nil {
			return err
		}
		if schema.SchemaClass == nil {
			return fmt.Errorf("schema class: %w", postgres.ErrInconsistentState)
		}
		entries, err := snapshot.StorageEntries(readCtx, version, artifactType)
		if err != nil {
			return err
		}
		if *schema.SchemaClass == "source-record" {
			if len(entries) != 0 {
				return fmt.Errorf("source record stores: %w", postgres.ErrInconsistentState)
			}
		} else if len(entries) == 0 {
			return fmt.Errorf("canonical stores: %w", postgres.ErrInconsistentState)
		}
		sort.Slice(entries, func(first, second int) bool { return entries[first].Store < entries[second].Store })
		result.Value = entries
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
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

// SchemaPackage resolves a selected schema and all bound contexts in one read snapshot.
func (s *Service) SchemaPackage(ctx context.Context, version, artifactType string) (Result[SchemaPackage], error) {
	var result Result[SchemaPackage]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		schema, err := loadDescriptor(readCtx, snapshot, version, "schema", artifactType)
		if err != nil {
			return err
		}
		if schema.Artifact.Payload.MediaType != "application/schema+json" {
			return fmt.Errorf("schema media type: %w", postgres.ErrInconsistentState)
		}
		bindings, err := snapshot.ContextBindings(readCtx, version, schema.Artifact.Revision)
		if err != nil {
			return err
		}
		contexts := make([]Descriptor, 0, len(bindings))
		seenContexts := make(map[string]struct{}, len(bindings))
		for _, binding := range bindings {
			if _, exists := seenContexts[binding.ContextRevision]; exists {
				return fmt.Errorf("duplicate context binding: %w", postgres.ErrInconsistentState)
			}
			seenContexts[binding.ContextRevision] = struct{}{}
			contextArtifact, err := snapshot.Artifact(readCtx, binding.ContextRevision)
			if err != nil || contextArtifact.Kind != "context" {
				return fmt.Errorf("load bound context: %w", postgres.ErrInconsistentState)
			}
			if contextArtifact.Payload.MediaType != `application/ld+json;profile="http://www.w3.org/ns/json-ld#context"` {
				return fmt.Errorf("context media type: %w", postgres.ErrInconsistentState)
			}
			resources, err := snapshot.Resources(readCtx, contextArtifact.Revision)
			if err != nil {
				return err
			}
			dependencies, err := snapshot.Dependencies(readCtx, contextArtifact.Revision)
			if err != nil {
				return err
			}
			contexts = append(contexts, Descriptor{Artifact: contextArtifact, Resources: resources, Dependencies: dependencies})
		}
		result.Value = SchemaPackage{Schema: schema, Contexts: contexts}
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// MappingPackage resolves the opaque program and both selected schema dependencies.
func (s *Service) MappingPackage(ctx context.Context, version, sourceType string) (Result[MappingPackage], error) {
	var result Result[MappingPackage]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		source, err := loadDescriptor(readCtx, snapshot, version, "schema", sourceType)
		if err != nil {
			return err
		}
		detail, err := snapshot.SourceMapping(readCtx, version, sourceType)
		if err != nil {
			return err
		}
		mapping, err := loadDescriptorByRevision(readCtx, snapshot, version, detail.MappingRevision)
		if err != nil || mapping.Artifact.Kind != "source-mapping" || detail.SourceRevision != source.Artifact.Revision {
			return fmt.Errorf("load mapping: %w", postgres.ErrInconsistentState)
		}
		target, err := loadDescriptor(readCtx, snapshot, version, "schema", detail.TargetArtifactType)
		if err != nil || target.Artifact.Revision != detail.TargetRevision {
			return fmt.Errorf("load mapping target: %w", postgres.ErrInconsistentState)
		}
		var program model.Resource
		for _, resource := range mapping.Resources {
			if resource.Name == detail.ProgramName {
				program = resource
				break
			}
		}
		if program.Name == "" || program.Role != "program" || program.Digest != detail.ProgramDigest {
			return fmt.Errorf("load mapping program: %w", postgres.ErrInconsistentState)
		}
		stores, err := snapshot.StorageEntries(readCtx, version, target.Artifact.Type)
		if err != nil {
			return err
		}
		if target.Artifact.SchemaClass == nil || *target.Artifact.SchemaClass == "source-record" || len(stores) == 0 {
			return fmt.Errorf("load target stores: %w", postgres.ErrInconsistentState)
		}
		result.Value = MappingPackage{Mapping: mapping, Detail: detail, Program: program, Source: source, Target: target, Stores: stores}
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// Mappings lists enriched mappings using resolved dependency metadata.
func (s *Service) Mappings(ctx context.Context, version string, filter model.ArtifactFilter, afterType *string, limit int) (Result[[]Mapping], error) {
	var result Result[[]Mapping]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		var afterKind *string
		if afterType != nil {
			kind := "source-mapping"
			afterKind = &kind
		}
		// Source-mapping predicates apply to resolved descriptor fields. Load the
		// bounded release collection before transport applies those predicates.
		items, err := snapshot.ReleaseArtifacts(readCtx, version, filter, afterKind, afterType, 10001)
		if err != nil {
			return err
		}
		mappings := make([]Mapping, 0, len(items))
		for _, item := range items {
			if item.Kind != "source-mapping" {
				continue
			}
			descriptor, err := loadDescriptorByArtifact(readCtx, snapshot, version, item)
			if err != nil {
				return err
			}
			detail, err := snapshot.SourceMappingByRevision(readCtx, version, item.Revision)
			if err != nil {
				return err
			}
			mappings = append(mappings, Mapping{Descriptor: descriptor, Detail: detail})
		}
		result.Value = mappings
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

func loadDescriptor(ctx context.Context, snapshot release.Snapshot, version, kind, artifactType string) (Descriptor, error) {
	value, err := snapshot.ReleaseArtifact(ctx, version, kind, artifactType)
	if err != nil {
		return Descriptor{}, err
	}
	return loadDescriptorByArtifact(ctx, snapshot, version, value)
}

func loadDescriptorByRevision(ctx context.Context, snapshot release.Snapshot, version, revision string) (Descriptor, error) {
	value, err := snapshot.Artifact(ctx, revision)
	if err != nil {
		return Descriptor{}, err
	}
	return loadDescriptorByArtifact(ctx, snapshot, version, value)
}

func loadDescriptorByArtifact(ctx context.Context, snapshot release.Snapshot, version string, value model.Artifact) (Descriptor, error) {
	if !validPayload(value.Payload) {
		return Descriptor{}, fmt.Errorf("artifact payload integrity: %w", postgres.ErrInconsistentState)
	}
	resources, err := snapshot.Resources(ctx, value.Revision)
	if err != nil {
		return Descriptor{}, err
	}
	dependencies, err := snapshot.Dependencies(ctx, value.Revision)
	if err != nil {
		return Descriptor{}, err
	}
	descriptor := Descriptor{Artifact: value, Resources: resources, Dependencies: dependencies}
	if value.Kind == "source-mapping" {
		detail, err := snapshot.SourceMappingByRevision(ctx, version, value.Revision)
		if err != nil {
			return Descriptor{}, err
		}
		descriptor.SourceArtifactType = detail.SourceArtifactType
	}
	return descriptor, nil
}

func validPayload(value model.ByteDescriptor) bool {
	digest := sha256.Sum256(value.Bytes)
	return int64(len(value.Bytes)) == value.Size && value.Digest == "sha-256:"+hex.EncodeToString(digest[:])
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
