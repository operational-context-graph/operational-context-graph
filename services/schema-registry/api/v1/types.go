// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package v1 defines the version 1 artifact and package JSON contract.
package v1

import "encoding/json"

// Page contains a collection of API resources and its pagination metadata.
type Page[T any] struct {
	SchemaVersion string            `json:"schemaVersion"`
	Items         []T               `json:"items"`
	NextCursor    *string           `json:"nextCursor"`
	Links         map[string]string `json:"links"`
}

// Descriptor describes an artifact without embedding its stored bytes.
type Descriptor struct {
	SchemaVersion             string            `json:"schemaVersion,omitempty"`
	Kind                      string            `json:"kind"`
	ArtifactType              string            `json:"artifactType"`
	ArtifactRevision          string            `json:"artifactRevision"`
	CreatedUnderSchemaVersion string            `json:"createdUnderSchemaVersion"`
	SchemaClass               *string           `json:"schemaClass,omitempty"`
	TargetSchemaVersion       *string           `json:"targetSchemaVersion,omitempty"`
	CanonicalURI              string            `json:"canonicalUri"`
	Payload                   Payload           `json:"payload"`
	Resources                 []Resource        `json:"resources"`
	Dependencies              []Dependency      `json:"dependencies"`
	Extensions                json.RawMessage   `json:"extensions,omitempty"`
	Links                     map[string]string `json:"links"`
}

// Payload identifies exact payload bytes and their immutable retrieval link.
type Payload struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}

// Resource describes one immutable named resource.
type Resource struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}

// Dependency identifies an exact dependency and its metadata link.
type Dependency struct {
	Kind             string `json:"kind"`
	ArtifactType     string `json:"artifactType"`
	ArtifactRevision string `json:"artifactRevision"`
	Href             string `json:"href"`
}

// SourceMapping is the complete, enriched source-mapping JSON model.
type SourceMapping struct {
	Descriptor
	SourceSchema SourceMappingSourceSchema `json:"sourceSchema"`
	Engine       SourceMappingEngine       `json:"engine"`
	Programs     SourceMappingPrograms     `json:"programs"`
}

// SourceMappingPrograms preserves an optional condition and ordered transformations.
type SourceMappingPrograms struct {
	Condition       *Resource                     `json:"condition,omitempty"`
	Transformations []SourceMappingTransformation `json:"transformations"`
}

// SourceMappingTransformation describes one program binding and its canonical output.
type SourceMappingTransformation struct {
	Resource
	TargetSchema SourceMappingSourceSchema `json:"targetSchema"`
	Cardinality  string                    `json:"cardinality"`
}

// SourceMappingSourceSchema resolves a schema reference within the selected release.
type SourceMappingSourceSchema struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
	Content          string `json:"content"`
}

// SourceMappingEngine carries opaque, publisher-declared engine metadata.
type SourceMappingEngine struct {
	ExecutionProfile string `json:"executionProfile"`
	Language         string `json:"language"`
	LanguageVersion  string `json:"languageVersion"`
}

// SchemaDocument embeds a JSON schema's complete metadata and parsed JSON value.
type SchemaDocument struct {
	Metadata Descriptor      `json:"metadata"`
	Document json.RawMessage `json:"document"`
}

// SourceMappingPackage is shared by direct package bodies and collection items.
type SourceMappingPackage struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Mapping       SourceMapping                 `json:"mapping"`
	Programs      []SourceMappingProgramContent `json:"programs"`
	Schemas       []SchemaDocument              `json:"schemas"`
	TargetStores  []string                      `json:"targetStores"`
	Links         map[string]string             `json:"links"`
}

// SourceMappingProgramContent embeds canonical Base64 content for one referenced program.
type SourceMappingProgramContent struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
}

// SchemaReference identifies a schema artifact revision.
type SchemaReference struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
}

// SchemaContexts contains the JSON-LD context descriptors for a schema.
type SchemaContexts struct {
	SchemaVersion string            `json:"schemaVersion"`
	Schema        SchemaReference   `json:"schema"`
	Items         []Descriptor      `json:"items"`
	Links         map[string]string `json:"links"`
}

// SchemaPackage contains a schema document and its context documents.
type SchemaPackage struct {
	SchemaVersion string            `json:"schemaVersion"`
	Schema        SchemaDocument    `json:"schema"`
	Contexts      []SchemaDocument  `json:"contexts"`
	Links         map[string]string `json:"links"`
}

// SchemaStores identifies the stores associated with a schema.
type SchemaStores struct {
	SchemaVersion string            `json:"schemaVersion"`
	Schema        SchemaStoreSchema `json:"schema"`
	Stores        []string          `json:"stores"`
	Links         map[string]string `json:"links"`
}

// SchemaStoreSchema identifies a schema in a stores response.
type SchemaStoreSchema struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
	SchemaClass      string `json:"schemaClass"`
}
