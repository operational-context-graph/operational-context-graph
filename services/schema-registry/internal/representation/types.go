// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package representation defines the shared version 1 artifact and package JSON contract.
package representation

import "encoding/json"

// DescriptorResponse describes an artifact without embedding its stored bytes.
type DescriptorResponse struct {
	SchemaVersion             string               `json:"schemaVersion,omitempty"`
	Kind                      string               `json:"kind"`
	ArtifactType              string               `json:"artifactType"`
	ArtifactRevision          string               `json:"artifactRevision"`
	CreatedUnderSchemaVersion string               `json:"createdUnderSchemaVersion"`
	SchemaClass               *string              `json:"schemaClass,omitempty"`
	TargetSchemaVersion       *string              `json:"targetSchemaVersion,omitempty"`
	CanonicalURI              string               `json:"canonicalUri"`
	Payload                   PayloadResponse      `json:"payload"`
	Resources                 []ResourceResponse   `json:"resources"`
	Dependencies              []DependencyResponse `json:"dependencies"`
	Extensions                json.RawMessage      `json:"extensions,omitempty"`
	Links                     map[string]string    `json:"links"`
}

// PayloadResponse identifies exact payload bytes and their immutable retrieval link.
type PayloadResponse struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}

// ResourceResponse describes one immutable named resource.
type ResourceResponse struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}

// DependencyResponse identifies an exact dependency and its metadata link.
type DependencyResponse struct {
	Kind             string `json:"kind"`
	ArtifactType     string `json:"artifactType"`
	ArtifactRevision string `json:"artifactRevision"`
	Href             string `json:"href"`
}

// MappingResponse is the complete, enriched source-mapping JSON model.
type MappingResponse struct {
	DescriptorResponse
	SourceSchema MappingSourceSchemaResponse `json:"sourceSchema"`
	Engine       MappingEngineResponse       `json:"engine"`
	Programs     MappingProgramsResponse     `json:"programs"`
}

// MappingProgramsResponse preserves an optional condition and ordered transformations.
type MappingProgramsResponse struct {
	Condition       *ResourceResponse               `json:"condition,omitempty"`
	Transformations []MappingTransformationResponse `json:"transformations"`
}

// MappingTransformationResponse describes one program binding and its canonical output.
type MappingTransformationResponse struct {
	ResourceResponse
	TargetSchema MappingSourceSchemaResponse `json:"targetSchema"`
	Cardinality  string                      `json:"cardinality"`
}

// MappingSourceSchemaResponse resolves a schema reference within the selected release.
type MappingSourceSchemaResponse struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
	Content          string `json:"content"`
}

// MappingEngineResponse carries opaque, publisher-declared engine metadata.
type MappingEngineResponse struct {
	ExecutionProfile string `json:"executionProfile"`
	Language         string `json:"language"`
	LanguageVersion  string `json:"languageVersion"`
}

// PackageSchemaResponse embeds a schema's complete metadata and parsed JSON value.
type PackageSchemaResponse struct {
	Metadata DescriptorResponse `json:"metadata"`
	Document json.RawMessage    `json:"document"`
}

// MappingPackageResponse is shared by direct package bodies and collection items.
type MappingPackageResponse struct {
	SchemaVersion string                          `json:"schemaVersion"`
	Mapping       MappingResponse                 `json:"mapping"`
	Programs      []MappingPackageProgramResponse `json:"programs"`
	Schemas       []PackageSchemaResponse         `json:"schemas"`
	TargetStores  []string                        `json:"targetStores"`
	Links         map[string]string               `json:"links"`
}

// MappingPackageProgramResponse embeds canonical Base64 content for one referenced program.
type MappingPackageProgramResponse struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
}
