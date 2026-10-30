// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package artifact implements read-only artifact-discovery use cases.
package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"sort"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	format "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// Service reads immutable artifacts through request-scoped snapshots.
type Service struct{ reader release.Reader }

// ErrReleaseNotFound distinguishes an invisible selected release from a missing artifact.
var ErrReleaseNotFound = errors.New("selected release not found")

// New creates an artifact-discovery service.
func New(reader release.Reader) *Service { return &Service{reader: reader} }

// Result preserves current and selected release metadata with a read result.
type Result[T any] struct {
	Current  model.CurrentState
	Selected string
	Value    T
}

// Descriptor is an artifact with its immutable resources and dependencies.
type Descriptor = model.Descriptor

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
type MappingPackage = model.MappingPackage

// Mapping is an enriched, release-selected source mapping.
type Mapping = model.Mapping

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
		if selected != "" {
			if _, err := snapshot.Release(readCtx, selected); err != nil {
				if errors.Is(err, postgres.ErrNotFound) {
					err = fmt.Errorf("%w: %w", ErrReleaseNotFound, err)
				}
				return requestError(current, "", err)
			}
		}
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

// MappingPackage resolves one mapping by its own logical artifact type.
func (s *Service) MappingPackage(ctx context.Context, version, mappingType string) (Result[MappingPackage], error) {
	var result Result[MappingPackage]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		mapping, err := loadMapping(readCtx, snapshot, version, mappingType, make(schemaMetadataCache))
		if err != nil {
			return err
		}
		result.Value, err = assembleMappingPackage(readCtx, snapshot, version, mapping)
		return err
	})
	result.Current, result.Selected = current, version
	return result, err
}

// MappingMetadata returns the shared enriched model without executing programs.
func (s *Service) MappingMetadata(ctx context.Context, version, mappingType string) (Result[Mapping], error) {
	var result Result[Mapping]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		var err error
		result.Value, err = loadMapping(readCtx, snapshot, version, mappingType, make(schemaMetadataCache))
		return err
	})
	result.Current, result.Selected = current, version
	return result, err
}

// schemaMetadataCache belongs to one read snapshot and one selected release only.
type schemaMetadataCache map[string]model.Artifact

func loadMapping(ctx context.Context, snapshot release.Snapshot, version, mappingType string, schemas schemaMetadataCache) (Mapping, error) {
	descriptor, err := loadDescriptor(ctx, snapshot, version, "source-mapping", mappingType)
	if err != nil {
		return Mapping{}, err
	}
	detail, err := snapshot.SourceMappingByRevision(ctx, version, descriptor.Artifact.Revision)
	if err != nil {
		return Mapping{}, mappingDependencyError(err)
	}
	parsed, err := format.Parse(descriptor.Artifact.Payload.Bytes)
	if err != nil || parsed.ID != descriptor.Artifact.Type || detail.MappingRevision != descriptor.Artifact.Revision || detail.SchemaVersion != version || parsed.SourceSchema.ID != detail.SourceArtifactType || descriptor.Artifact.TargetSchemaVersion == nil || *descriptor.Artifact.TargetSchemaVersion != detail.TargetSchemaVersion {
		return Mapping{}, fmt.Errorf("mapping descriptor agreement: %w", postgres.ErrInconsistentState)
	}
	if parsed.Engine.Language != detail.EngineLanguage || parsed.Engine.LanguageVersion != detail.EngineLanguageVersion || parsed.Engine.ExecutionProfile != detail.ExecutionProfile || len(parsed.Programs.Transformations) != len(detail.Transformations) || (parsed.Programs.Condition == nil) != (detail.Condition == nil) {
		return Mapping{}, postgres.ErrInconsistentState
	}
	resources := make(map[string]model.Resource, len(descriptor.Resources))
	for _, resource := range descriptor.Resources {
		_, _, mediaErr := mime.ParseMediaType(resource.MediaType)
		if _, duplicate := resources[resource.Name]; duplicate || resource.ArtifactRevision != detail.MappingRevision || !artifactmodel.ValidLogicalType(resource.Role) || !artifactmodel.ValidResourceName(resource.Name) || mediaErr != nil || len(resource.MediaType) > artifactmodel.MaxIdentifierBytes || resource.Size < 0 || resource.Size > artifactmodel.MaxArtifactBytes || !artifactmodel.ValidDigest(resource.Digest) || resource.Bytes != nil && !validPayload(resource.ByteDescriptor) {
			return Mapping{}, postgres.ErrInconsistentState
		}
		resources[resource.Name] = resource
	}
	referenced := make(map[string]bool)
	bind := func(reference format.ProgramReference, projected model.ProgramBinding) bool {
		resource, exists := resources[reference.ProgramRef]
		referenced[reference.ProgramRef] = true
		return exists && resource.Role == "program" && reference.ProgramRef == projected.ProgramName && reference.ProgramDigest == projected.ProgramDigest && resource.Digest == reference.ProgramDigest
	}
	resolve := func(reference format.SchemaReference, artifactType, revision string, target bool) error {
		if reference.ID != artifactType || reference.Revision != "" && reference.Revision != revision || target && reference.Version != "" && reference.Version != detail.TargetSchemaVersion {
			return postgres.ErrInconsistentState
		}
		selected, cached := schemas[artifactType]
		if !cached {
			var err error
			selected, err = selectedSchemaMetadata(ctx, snapshot, version, artifactType)
			if err != nil {
				return mappingDependencyError(err)
			}
			selected.Payload.Bytes = nil
			schemas[artifactType] = selected
		}
		if selected.Revision != revision {
			return postgres.ErrInconsistentState
		}
		if target && (selected.SchemaClass == nil || *selected.SchemaClass != "entity-type" && *selected.SchemaClass != "identifier-type" && *selected.SchemaClass != "edge-type") {
			return postgres.ErrInconsistentState
		}
		for _, dependency := range descriptor.Dependencies {
			if dependency.Kind == "schema" && dependency.ArtifactType == artifactType && dependency.Revision == revision {
				return nil
			}
		}
		return postgres.ErrInconsistentState
	}
	if err := resolve(parsed.SourceSchema, detail.SourceArtifactType, detail.SourceRevision, false); err != nil {
		return Mapping{}, err
	}
	if parsed.Programs.Condition != nil && !bind(*parsed.Programs.Condition, *detail.Condition) {
		return Mapping{}, postgres.ErrInconsistentState
	}
	for index, transformation := range parsed.Programs.Transformations {
		projected := detail.Transformations[index]
		if transformation.Cardinality != projected.Cardinality || !bind(transformation.ProgramReference, projected.ProgramBinding) {
			return Mapping{}, postgres.ErrInconsistentState
		}
		if err := resolve(transformation.TargetSchema, projected.TargetArtifactType, projected.TargetRevision, true); err != nil {
			return Mapping{}, err
		}
	}
	for name, resource := range resources {
		if resource.Role == "program" && !referenced[name] {
			return Mapping{}, postgres.ErrInconsistentState
		}
	}
	descriptor.Artifact.Payload.Bytes = nil
	for index := range descriptor.Resources {
		descriptor.Resources[index].Bytes = nil
	}
	return Mapping{Descriptor: descriptor, Detail: detail}, nil
}

func selectedSchemaMetadata(ctx context.Context, snapshot release.Snapshot, version, artifactType string) (model.Artifact, error) {
	if reader, ok := snapshot.(interface {
		ReleaseArtifactMetadata(context.Context, string, string, string) (model.Artifact, error)
	}); ok {
		return reader.ReleaseArtifactMetadata(ctx, version, "schema", artifactType)
	}
	return snapshot.ReleaseArtifact(ctx, version, "schema", artifactType)
}

func mappingDependencyError(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return fmt.Errorf("mapping dependency: %w", postgres.ErrInconsistentState)
	}
	return err
}

func assembleMappingPackage(ctx context.Context, snapshot release.Snapshot, version string, mapping Mapping) (MappingPackage, error) {
	programNames := make(map[string]bool)
	if condition := mapping.Detail.Condition; condition != nil {
		programNames[condition.ProgramName] = true
	}
	for _, transformation := range mapping.Detail.Transformations {
		programNames[transformation.ProgramName] = true
	}
	result := MappingPackage{Mapping: mapping.Descriptor, Detail: mapping.Detail, Programs: make([]model.Resource, 0, len(programNames))}
	for _, resource := range mapping.Descriptor.Resources {
		if programNames[resource.Name] {
			result.Programs = append(result.Programs, resource)
		}
	}
	if len(result.Programs) != len(programNames) {
		return MappingPackage{}, postgres.ErrInconsistentState
	}
	var budget int64
	for _, resource := range result.Programs {
		budget += (resource.Size + 2) / 3 * 4
	}
	if budget > config.MaxGeneratedJSONBytes {
		return MappingPackage{}, postgres.ErrInconsistentState
	}
	for index, resource := range result.Programs {
		loaded, err := snapshot.Resource(ctx, resource.ArtifactRevision, resource.Name)
		if err != nil {
			return MappingPackage{}, mappingDependencyError(err)
		}
		if loaded.Name != resource.Name || loaded.ArtifactRevision != resource.ArtifactRevision || loaded.Role != resource.Role || loaded.MediaType != resource.MediaType || loaded.Size != resource.Size || loaded.Digest != resource.Digest || !validPayload(loaded.ByteDescriptor) {
			return MappingPackage{}, postgres.ErrInconsistentState
		}
		result.Programs[index] = loaded
	}
	source, err := loadDescriptor(ctx, snapshot, version, "schema", mapping.Detail.SourceArtifactType)
	if err != nil {
		return MappingPackage{}, mappingDependencyError(err)
	}
	if source.Artifact.Payload.MediaType != "application/schema+json" {
		return MappingPackage{}, postgres.ErrInconsistentState
	}
	source, err = compactSchemaDocument(source)
	if err != nil {
		return MappingPackage{}, err
	}
	budget += int64(len(source.Artifact.Payload.Bytes))
	if budget > config.MaxGeneratedJSONBytes {
		return MappingPackage{}, postgres.ErrInconsistentState
	}
	result.Schemas = []Descriptor{source}
	seenSchemas := map[string]bool{source.Artifact.Revision: true}
	seenStores := make(map[string]bool)
	for _, transformation := range mapping.Detail.Transformations {
		if seenSchemas[transformation.TargetRevision] {
			continue
		}
		target, err := loadDescriptor(ctx, snapshot, version, "schema", transformation.TargetArtifactType)
		if err != nil {
			return MappingPackage{}, mappingDependencyError(err)
		}
		if target.Artifact.Revision != transformation.TargetRevision || target.Artifact.Payload.MediaType != "application/schema+json" {
			return MappingPackage{}, postgres.ErrInconsistentState
		}
		if !seenSchemas[target.Artifact.Revision] {
			target, err = compactSchemaDocument(target)
			if err != nil {
				return MappingPackage{}, err
			}
			budget += int64(len(target.Artifact.Payload.Bytes))
			if budget > config.MaxGeneratedJSONBytes {
				return MappingPackage{}, postgres.ErrInconsistentState
			}
			result.Schemas = append(result.Schemas, target)
			seenSchemas[target.Artifact.Revision] = true
		}
		stores, err := snapshot.StorageEntries(ctx, version, target.Artifact.Type)
		if err != nil {
			return MappingPackage{}, err
		}
		if len(stores) == 0 {
			return MappingPackage{}, postgres.ErrInconsistentState
		}
		for _, store := range stores {
			if store.SchemaVersion != version || store.ArtifactType != target.Artifact.Type {
				return MappingPackage{}, postgres.ErrInconsistentState
			}
			if !seenStores[store.Store] {
				result.Stores = append(result.Stores, store)
				seenStores[store.Store] = true
			}
		}
	}
	// A canonical source can also be a target. Its schema stays deduplicated,
	// while its target storage assignment still contributes to the union.
	for _, transformation := range mapping.Detail.Transformations {
		if transformation.TargetRevision != source.Artifact.Revision {
			continue
		}
		stores, err := snapshot.StorageEntries(ctx, version, source.Artifact.Type)
		if err != nil {
			return MappingPackage{}, err
		}
		if len(stores) == 0 {
			return MappingPackage{}, postgres.ErrInconsistentState
		}
		for _, store := range stores {
			if !seenStores[store.Store] {
				result.Stores = append(result.Stores, store)
				seenStores[store.Store] = true
			}
		}
		break
	}
	sort.Slice(result.Schemas[1:], func(i, j int) bool {
		a, b := result.Schemas[i+1].Artifact, result.Schemas[j+1].Artifact
		if a.Type == b.Type {
			return a.Revision < b.Revision
		}
		return a.Type < b.Type
	})
	sort.Slice(result.Programs, func(i, j int) bool { return result.Programs[i].Name < result.Programs[j].Name })
	sort.Slice(result.Stores, func(i, j int) bool { return result.Stores[i].Store < result.Stores[j].Store })
	return result, nil
}

func compactSchemaDocument(value Descriptor) (Descriptor, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, value.Artifact.Payload.Bytes); err != nil {
		return Descriptor{}, postgres.ErrInconsistentState
	}
	value.Artifact.Payload.Bytes = compact.Bytes()
	return value, nil
}

// Mappings lists enriched mappings using resolved dependency metadata.
func (s *Service) Mappings(ctx context.Context, version string, filter model.ArtifactFilter, afterType *string, limit int) (Result[[]Mapping], error) {
	var result Result[[]Mapping]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		for _, schemaType := range []string{filter.SourceArtifactType, filter.TargetArtifactType} {
			if schemaType != "" {
				if _, err := snapshot.ReleaseArtifact(readCtx, version, "schema", schemaType); err != nil {
					return err
				}
			}
		}
		var afterKind *string
		if afterType != nil {
			kind := "source-mapping"
			afterKind = &kind
		}
		items, err := snapshot.ReleaseArtifacts(readCtx, version, filter, afterKind, afterType, limit)
		if err != nil {
			return err
		}
		mappings := make([]Mapping, 0, len(items))
		schemas := make(schemaMetadataCache)
		var minimumBytes int64
		for index, item := range items {
			if item.Kind != "source-mapping" {
				continue
			}
			if index == limit-1 {
				mappings = append(mappings, Mapping{Descriptor: Descriptor{Artifact: item}})
				break
			}
			mapping, err := loadMapping(readCtx, snapshot, version, item.Type, schemas)
			if err != nil {
				return err
			}
			minimumBytes += minimumMappingBytes(mapping, version)
			if minimumBytes > config.MaxGeneratedJSONBytes {
				return postgres.ErrInconsistentState
			}
			mappings = append(mappings, mapping)
		}
		result.Value = mappings
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

// MappingPackages assembles an entire candidate page in the same read snapshot.
func (s *Service) MappingPackages(ctx context.Context, version string, filter model.ArtifactFilter, afterType *string, limit int) (Result[[]MappingPackage], error) {
	var result Result[[]MappingPackage]
	current, err := s.read(ctx, version, func(readCtx context.Context, snapshot release.Snapshot) error {
		if _, err := snapshot.ReleaseArtifact(readCtx, version, "schema", filter.SourceArtifactType); err != nil {
			return err
		}
		var afterKind *string
		if afterType != nil {
			kind := "source-mapping"
			afterKind = &kind
		}
		items, err := snapshot.ReleaseArtifacts(readCtx, version, filter, afterKind, afterType, limit)
		if err != nil {
			return err
		}
		result.Value = make([]MappingPackage, 0, len(items))
		schemas := make(schemaMetadataCache)
		var minimumBytes int64
		for index, item := range items {
			if index == limit-1 {
				result.Value = append(result.Value, MappingPackage{Mapping: Descriptor{Artifact: item}})
				break
			}
			mapping, err := loadMapping(readCtx, snapshot, version, item.Type, schemas)
			if err != nil {
				return err
			}
			value, err := assembleMappingPackage(readCtx, snapshot, version, mapping)
			if err != nil {
				return err
			}
			minimumBytes += minimumMappingBytes(mapping, version)
			for _, program := range value.Programs {
				minimumBytes += (program.Size + 2) / 3 * 4
			}
			for _, schema := range value.Schemas {
				minimumBytes += int64(len(schema.Artifact.Payload.Bytes))
				minimumBytes += extensionBytes(schema.Artifact.Extensions)
			}
			if minimumBytes > config.MaxGeneratedJSONBytes {
				return postgres.ErrInconsistentState
			}
			result.Value = append(result.Value, value)
		}
		return nil
	})
	result.Current, result.Selected = current, version
	return result, err
}

func extensionBytes(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	encoded, err := json.Marshal(json.RawMessage(raw))
	if err != nil {
		return config.MaxGeneratedJSONBytes + 1
	}
	return int64(len(encoded))
}

// Every counted value occurs in the wire model. This lower bound limits retained
// domain records; transport verifies each complete item and the final envelope.
func minimumMappingBytes(mapping Mapping, version string) int64 {
	size := extensionBytes(mapping.Descriptor.Artifact.Extensions)
	for _, transformation := range mapping.Detail.Transformations {
		size += int64(len(version) + len(transformation.TargetArtifactType) + len(transformation.TargetRevision) + len(transformation.ProgramName) + len(transformation.ProgramDigest))
	}
	return size
}

func loadDescriptor(ctx context.Context, snapshot release.Snapshot, version, kind, artifactType string) (Descriptor, error) {
	value, err := snapshot.ReleaseArtifact(ctx, version, kind, artifactType)
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
			return Descriptor{}, mappingDependencyError(err)
		}
		descriptor.SourceArtifactType = detail.SourceArtifactType
	}
	return descriptor, nil
}

func validPayload(value model.ByteDescriptor) bool {
	digest := sha256.Sum256(value.Bytes)
	return value.Size >= 0 && value.Size <= artifactmodel.MaxArtifactBytes && int64(len(value.Bytes)) == value.Size && value.Digest == "sha-256:"+hex.EncodeToString(digest[:])
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
