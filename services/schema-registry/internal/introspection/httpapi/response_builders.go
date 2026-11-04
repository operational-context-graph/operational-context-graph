// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/api/v1"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

// BuildDescriptor projects validated artifact metadata into the shared wire contract.
func BuildDescriptor(value model.Descriptor, selected string) v1.Descriptor {
	item := value.Artifact
	result := v1.Descriptor{Kind: item.Kind, ArtifactType: item.Type, ArtifactRevision: item.Revision, CreatedUnderSchemaVersion: item.CreatedUnderSchemaVersion, SchemaClass: item.SchemaClass, TargetSchemaVersion: item.TargetSchemaVersion, CanonicalURI: item.CanonicalURI, Payload: v1.Payload{MediaType: item.Payload.MediaType, Digest: item.Payload.Digest, Size: item.Payload.Size, Href: "/v1/artifacts/" + item.Revision}, Resources: make([]v1.Resource, 0, len(value.Resources)), Dependencies: make([]v1.Dependency, 0, len(value.Dependencies)), Links: map[string]string{"content": "/v1/artifacts/" + item.Revision, "history": "/v1/artifact-kinds/" + item.Kind + "/" + item.Type + "/history"}}
	if selected == "" {
		result.Links["self"] = "/v1/artifacts/" + item.Revision + "/metadata"
	} else {
		result.SchemaVersion = selected
		result.Links["self"] = "/v1/releases/" + selected + "/artifacts/" + item.Kind + "/" + item.Type
	}
	if len(item.Extensions) > 0 {
		result.Extensions = append(json.RawMessage(nil), item.Extensions...)
	}
	for _, resource := range value.Resources {
		result.Resources = append(result.Resources, resourceMetadata(item.Revision, resource))
	}
	for _, dependency := range value.Dependencies {
		href := "/v1/artifacts/" + dependency.Revision + "/metadata"
		if selected != "" {
			href = "/v1/releases/" + selected + "/artifacts/" + dependency.Kind + "/" + dependency.ArtifactType
		}
		result.Dependencies = append(result.Dependencies, v1.Dependency{Kind: dependency.Kind, ArtifactType: dependency.ArtifactType, ArtifactRevision: dependency.Revision, Href: href})
	}
	if selected != "" && item.Kind == "schema" {
		base := "/v1/releases/" + selected + "/schemas/" + item.Type
		result.Links["contexts"], result.Links["package"], result.Links["sourceMappings"], result.Links["stores"] = base+"/contexts", base+"/package", "/v1/releases/"+selected+"/source-mappings?sourceArtifactType="+item.Type, base+"/stores"
	}
	if selected != "" && item.Kind == "source-mapping" {
		result.Links["self"] = "/v1/releases/" + selected + "/source-mappings/" + item.Type
		result.Links["package"] = result.Links["self"] + "/package"
	}
	return result
}

func resourceMetadata(revision string, resource model.Resource) v1.Resource {
	return v1.Resource{Name: resource.Name, Role: resource.Role, MediaType: resource.MediaType, Digest: resource.Digest, Size: resource.Size, Href: "/v1/artifacts/" + revision + "/resources/" + resource.Name}
}

// BuildMapping projects an already-validated mapping without interpreting its programs.
func BuildMapping(value model.SourceMapping, version string) v1.SourceMapping {
	resources := make(map[string]v1.Resource, len(value.Descriptor.Resources))
	for _, resource := range value.Descriptor.Resources {
		resources[resource.Name] = resourceMetadata(value.Descriptor.Artifact.Revision, resource)
	}
	result := v1.SourceMapping{Descriptor: BuildDescriptor(value.Descriptor, version), SourceSchema: schemaReference(version, value.Detail.SourceArtifactType, value.Detail.SourceRevision), Engine: v1.SourceMappingEngine{Language: value.Detail.EngineLanguage, LanguageVersion: value.Detail.EngineLanguageVersion, ExecutionProfile: value.Detail.ExecutionProfile}, Programs: v1.SourceMappingPrograms{Transformations: make([]v1.SourceMappingTransformation, 0, len(value.Detail.Transformations))}}
	if value.Detail.Condition != nil {
		resource := resources[value.Detail.Condition.ProgramName]
		result.Programs.Condition = &resource
	}
	for _, transformation := range value.Detail.Transformations {
		result.Programs.Transformations = append(result.Programs.Transformations, v1.SourceMappingTransformation{Resource: resources[transformation.ProgramName], Cardinality: transformation.Cardinality, TargetSchema: schemaReference(version, transformation.TargetArtifactType, transformation.TargetRevision)})
	}
	return result
}

func schemaReference(version, artifactType, revision string) v1.SourceMappingSourceSchema {
	return v1.SourceMappingSourceSchema{ArtifactType: artifactType, ArtifactRevision: revision, Content: "/v1/releases/" + version + "/schemas/" + artifactType + "/content"}
}

// mappingPackageMetadata creates the actual wire model, leaving only embedded content empty.
// It includes all metadata and links before adding embedded content.
func mappingPackageMetadata(value model.SourceMappingPackage, version string) (v1.SourceMappingPackage, error) {
	if len(value.Schemas) == 0 || len(value.Programs) == 0 {
		return v1.SourceMappingPackage{}, fmt.Errorf("%w: mapping package content is missing", ErrInvalid)
	}
	base := "/v1/releases/" + version + "/source-mappings/" + value.Mapping.Artifact.Type
	body := v1.SourceMappingPackage{SchemaVersion: version, Mapping: BuildMapping(model.SourceMapping{Descriptor: value.Mapping, Detail: value.Detail}, version), Schemas: make([]v1.PackageSchema, 0, len(value.Schemas)), Programs: make([]v1.SourceMappingPackageProgram, 0, len(value.Programs)), TargetStores: make([]string, 0, len(value.Stores)), Links: map[string]string{"self": base + "/package", "mapping": base, "descriptor": "/v1/artifacts/" + value.Mapping.Artifact.Revision}}
	for _, schema := range value.Schemas {
		body.Schemas = append(body.Schemas, v1.PackageSchema{Metadata: BuildDescriptor(schema, version)})
	}
	for _, program := range value.Programs {
		body.Programs = append(body.Programs, v1.SourceMappingPackageProgram{Name: program.Name, Role: program.Role, MediaType: program.MediaType, Digest: program.Digest, Size: program.Size})
	}
	for _, store := range value.Stores {
		body.TargetStores = append(body.TargetStores, store.Store)
	}
	return body, nil
}

// BuildMappingPackage checks identity size before allocating Base64 program strings.
// Callers MUST validate release consistency and byte integrity before calling this function.
func BuildMappingPackage(value model.SourceMappingPackage, version string) (v1.SourceMappingPackage, error) {
	body, err := mappingPackageMetadata(value, version)
	if err != nil {
		return v1.SourceMappingPackage{}, err
	}
	if _, err := MappingPackageSize(value, version); err != nil {
		return v1.SourceMappingPackage{}, err
	}
	for index, schema := range value.Schemas {
		body.Schemas[index].Document = append(json.RawMessage(nil), schema.Artifact.Payload.Bytes...)
	}
	for index, program := range value.Programs {
		if int64(len(program.Bytes)) != program.Size {
			return v1.SourceMappingPackage{}, fmt.Errorf("%w: program byte length differs from its metadata", ErrInvalid)
		}
		body.Programs[index].Content = base64.StdEncoding.EncodeToString(program.Bytes)
	}
	return body, nil
}
