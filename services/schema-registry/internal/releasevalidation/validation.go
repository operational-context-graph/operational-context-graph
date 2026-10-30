// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package releasevalidation validates release format and application response serviceability.
package releasevalidation

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/representation"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/source-mapping"
)

// Validate checks immutable format constraints before application response limits.
func Validate(value manifest.Manifest, context manifest.Context) error {
	if err := value.Validate(context); err != nil {
		return err
	}
	closure := indexClosure(value, context)
	for _, selection := range value.Artifacts {
		if selection.Kind != artifact.KindSourceMapping {
			continue
		}
		pack, err := closure.mappingPackage(selection)
		if err != nil {
			return fmt.Errorf("source mapping %s package: %w", selection.Type, err)
		}
		if _, err := representation.MappingPackageSize(pack, closure.version); err != nil {
			return fmt.Errorf("source mapping %s package: %w", selection.Type, err)
		}
	}
	return nil
}

// selectedClosure indexes only the containing release after format validation succeeds.
type selectedClosure struct {
	version     string
	context     manifest.Context
	selections  map[manifest.Key]manifest.Artifact
	schemas     map[string]manifest.Artifact
	descriptors map[manifest.Key]model.Descriptor
	stores      map[string][]model.StorageEntry
}

func indexClosure(value manifest.Manifest, context manifest.Context) *selectedClosure {
	closure := &selectedClosure{
		version: value.SchemaVersion, context: context,
		selections:  make(map[manifest.Key]manifest.Artifact, len(value.Artifacts)),
		schemas:     make(map[string]manifest.Artifact),
		descriptors: make(map[manifest.Key]model.Descriptor),
		stores:      make(map[string][]model.StorageEntry, len(value.StorageCatalog)),
	}
	for _, selection := range value.Artifacts {
		closure.selections[selectionKey(selection)] = selection
		if selection.Kind == artifact.KindSchema {
			closure.schemas[selection.Type] = selection
		}
	}
	for _, entry := range value.StorageCatalog {
		for _, store := range entry.Stores {
			closure.stores[entry.ArtifactType] = append(closure.stores[entry.ArtifactType], model.StorageEntry{
				SchemaVersion: value.SchemaVersion, ArtifactType: entry.ArtifactType, Store: store,
			})
		}
	}
	return closure
}

func selectionKey(value manifest.Artifact) manifest.Key {
	return manifest.Key{Kind: value.Kind, Type: value.Type, Revision: value.Revision}
}

func (c *selectedClosure) descriptor(key manifest.Key) (model.Descriptor, error) {
	if descriptor, exists := c.descriptors[key]; exists {
		return descriptor, nil
	}
	selection, exists := c.selections[key]
	if !exists {
		return model.Descriptor{}, fmt.Errorf("%w: artifact %s/%s/%s selection is missing", representation.ErrInvalid, key.Kind, key.Type, key.Revision)
	}
	bound, exists := c.context.Artifacts[key]
	if !exists || len(bound.PayloadBytes) == 0 {
		return model.Descriptor{}, fmt.Errorf("%w: artifact %s/%s/%s bytes are missing", representation.ErrInvalid, key.Kind, key.Type, key.Revision)
	}
	value := model.Descriptor{
		Artifact: model.Artifact{
			Kind: string(selection.Kind), Type: selection.Type, Revision: selection.Revision,
			CreatedUnderSchemaVersion: selection.CreatedUnderSchemaVersion,
			CanonicalURI:              selection.CanonicalURI,
			Payload:                   byteDescriptor(selection.Payload, bound.PayloadBytes),
		},
		Resources:    make([]model.Resource, 0, len(selection.Resources)),
		Dependencies: make([]model.Dependency, 0, len(selection.Dependencies)),
	}
	if selection.SchemaClass != "" {
		class := string(selection.SchemaClass)
		value.Artifact.SchemaClass = &class
	}
	if selection.TargetSchema != "" {
		version := selection.TargetSchema
		value.Artifact.TargetSchemaVersion = &version
	}
	if selection.Extensions != nil {
		extensions, err := json.Marshal(selection.Extensions)
		if err != nil {
			return model.Descriptor{}, fmt.Errorf("%w: artifact extensions: %v", representation.ErrInvalid, err)
		}
		value.Artifact.Extensions = extensions
	}
	resources := make(map[string]artifact.Resource, len(bound.Resources))
	for _, resource := range bound.Resources {
		resources[resource.Name] = resource
	}
	for _, resource := range selection.Resources {
		if _, exists := resources[resource.Name]; !exists {
			return model.Descriptor{}, fmt.Errorf("%w: artifact %s resource %s is missing", representation.ErrInvalid, key.Type, resource.Name)
		}
		// Size validation needs declared program sizes, not retained resource bytes.
		value.Resources = append(value.Resources, model.Resource{
			ArtifactRevision: key.Revision, Name: resource.Name, Role: resource.Role,
			ByteDescriptor: byteDescriptor(resource.Descriptor, nil),
		})
	}
	for _, dependency := range selection.Dependencies {
		value.Dependencies = append(value.Dependencies, model.Dependency{
			ArtifactRevision: key.Revision, Kind: string(dependency.Kind),
			ArtifactType: dependency.Type, Revision: dependency.Revision,
		})
	}
	c.descriptors[key] = value
	return value, nil
}

func byteDescriptor(value artifact.Descriptor, data []byte) model.ByteDescriptor {
	return model.ByteDescriptor{MediaType: value.MediaType, Digest: value.Digest, Size: value.Size, Bytes: data}
}

func (c *selectedClosure) schema(reference sourcemapping.SchemaReference) (model.Descriptor, error) {
	selection, exists := c.schemas[reference.ID]
	if !exists || reference.Revision != "" && selection.Revision != reference.Revision {
		return model.Descriptor{}, fmt.Errorf("%w: selected schema %s/%s is missing", representation.ErrInvalid, reference.ID, reference.Revision)
	}
	// Format validation proves every version selector resolves to this selected revision.
	return c.descriptor(selectionKey(selection))
}

func (c *selectedClosure) mappingPackage(selection manifest.Artifact) (model.MappingPackage, error) {
	mapping, err := c.descriptor(selectionKey(selection))
	if err != nil {
		return model.MappingPackage{}, err
	}
	parsed, err := sourcemapping.Parse(mapping.Artifact.Payload.Bytes)
	if err != nil {
		return model.MappingPackage{}, fmt.Errorf("%w: parse mapping descriptor: %v", representation.ErrInvalid, err)
	}
	source, err := c.schema(parsed.SourceSchema)
	if err != nil {
		return model.MappingPackage{}, err
	}
	mapping.SourceArtifactType = source.Artifact.Type
	detail := model.SourceMappingDetail{
		SchemaVersion: c.version, MappingRevision: selection.Revision,
		SourceArtifactType: source.Artifact.Type, SourceRevision: source.Artifact.Revision,
		TargetSchemaVersion: selection.TargetSchema,
		EngineLanguage:      parsed.Engine.Language, EngineLanguageVersion: parsed.Engine.LanguageVersion,
		ExecutionProfile: parsed.Engine.ExecutionProfile,
		Transformations:  make([]model.MappingTransformation, 0, len(parsed.Programs.Transformations)),
	}
	resources := make(map[string]model.Resource, len(mapping.Resources))
	for _, resource := range mapping.Resources {
		resources[resource.Name] = resource
	}
	programs := make(map[string]model.Resource)
	bind := func(reference sourcemapping.ProgramReference) (model.ProgramBinding, error) {
		resource, exists := resources[reference.ProgramRef]
		if !exists || resource.Role != "program" || resource.Digest != reference.ProgramDigest {
			return model.ProgramBinding{}, fmt.Errorf("%w: program resource %s is missing or disagrees with binding", representation.ErrInvalid, reference.ProgramRef)
		}
		programs[resource.Name] = resource
		return model.ProgramBinding{ProgramName: resource.Name, ProgramDigest: resource.Digest}, nil
	}
	if parsed.Programs.Condition != nil {
		condition, err := bind(*parsed.Programs.Condition)
		if err != nil {
			return model.MappingPackage{}, err
		}
		detail.Condition = &condition
	}
	result := model.MappingPackage{Mapping: mapping, Schemas: []model.Descriptor{source}}
	seenSchemas := map[string]bool{source.Artifact.Revision: true}
	stores := make(map[string]model.StorageEntry)
	for _, transformation := range parsed.Programs.Transformations {
		target, err := c.schema(transformation.TargetSchema)
		if err != nil {
			return model.MappingPackage{}, err
		}
		program, err := bind(transformation.ProgramReference)
		if err != nil {
			return model.MappingPackage{}, err
		}
		detail.Transformations = append(detail.Transformations, model.MappingTransformation{
			ProgramBinding: program, TargetArtifactType: target.Artifact.Type,
			TargetRevision: target.Artifact.Revision, Cardinality: transformation.Cardinality,
		})
		if !seenSchemas[target.Artifact.Revision] {
			result.Schemas = append(result.Schemas, target)
			seenSchemas[target.Artifact.Revision] = true
		}
		placements := c.stores[target.Artifact.Type]
		if len(placements) == 0 {
			return model.MappingPackage{}, fmt.Errorf("%w: target schema %s stores are missing", representation.ErrInvalid, target.Artifact.Type)
		}
		for _, store := range placements {
			if _, exists := stores[store.Store]; !exists {
				stores[store.Store] = store
			}
		}
	}
	result.Detail = detail
	result.Programs = make([]model.Resource, 0, len(programs))
	for _, program := range programs {
		result.Programs = append(result.Programs, program)
	}
	result.Stores = make([]model.StorageEntry, 0, len(stores))
	for _, store := range stores {
		result.Stores = append(result.Stores, store)
	}
	sort.Slice(result.Programs, func(i, j int) bool { return result.Programs[i].Name < result.Programs[j].Name })
	sort.Slice(result.Schemas[1:], func(i, j int) bool {
		left, right := result.Schemas[i+1].Artifact, result.Schemas[j+1].Artifact
		if left.Type == right.Type {
			return left.Revision < right.Revision
		}
		return left.Type < right.Type
	})
	sort.Slice(result.Stores, func(i, j int) bool { return result.Stores[i].Store < result.Stores[j].Store })
	return result, nil
}
