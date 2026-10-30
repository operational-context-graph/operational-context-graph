// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package model defines immutable records shared between storage and services.
package model

// Release is an immutable imported release with pre-populated visibility state.
type Release struct {
	SchemaVersion          string
	ManifestDigest         string
	ManifestBytes          []byte
	PreviousSchemaVersion  *string
	PreviousManifestDigest *string
	SourceRepository       string
	SourceCommit           string
	Changes                []byte
	ProjectionCommitment   []byte
	// ActivationSequence orders externally supplied activated releases.
	ActivationSequence int64
}

// CurrentState is the current visible release pointer from one read snapshot.
type CurrentState struct {
	SchemaVersion      string
	ActivationSequence int64
	SnapshotSequence   int64
}

// Artifact is an immutable revision-bound record.
type Artifact struct {
	Revision                  string
	Kind                      string
	Type                      string
	CreatedUnderSchemaVersion string
	SchemaClass               *string
	TargetSchemaVersion       *string
	CanonicalURI              string
	Payload                   ByteDescriptor
	Extensions                []byte
}

// ByteDescriptor describes exact stored bytes.
type ByteDescriptor struct {
	MediaType string
	Digest    string
	Size      int64
	Bytes     []byte
}

// Resource is an immutable named artifact resource.
type Resource struct {
	ArtifactRevision string
	Name             string
	Role             string
	ByteDescriptor
}

// Dependency identifies one exact revision dependency.
type Dependency struct {
	ArtifactRevision string
	Kind             string
	ArtifactType     string
	Revision         string
}

// ReleaseArtifact selects a revision for a logical identity in one release.
type ReleaseArtifact struct {
	SchemaVersion      string
	Kind               string
	ArtifactType       string
	ArtifactRevision   string
	ActivationSequence int64
}

// ArtifactFilter contains exact, release-scoped artifact collection filters.
type ArtifactFilter struct {
	Kind               string
	ArtifactType       string
	SchemaClass        string
	DependsOnRevision  string
	SourceArtifactType string
	TargetArtifactType string
	EngineLanguage     string
	ExecutionProfile   string
}

// ContextBinding binds a selected schema revision to a selected context revision.
type ContextBinding struct {
	SchemaVersion   string
	SchemaRevision  string
	ContextRevision string
}

// URIEntry resolves an exact URI or IRI within one release.
type URIEntry struct {
	SchemaVersion    string
	URI              string
	ArtifactRevision string
	ResourceName     *string
}

// StorageEntry assigns one schema identity to a logical OCG store.
type StorageEntry struct {
	SchemaVersion string
	ArtifactType  string
	Store         string
}

// SourceMappingDetail holds descriptor fields resolved to exact dependencies.
type SourceMappingDetail struct {
	SchemaVersion         string
	MappingRevision       string
	SourceArtifactType    string
	SourceRevision        string
	TargetSchemaVersion   string
	EngineLanguage        string
	EngineLanguageVersion string
	ExecutionProfile      string
	Condition             *ProgramBinding
	Transformations       []MappingTransformation
}

// ProgramBinding identifies one opaque resource owned by a mapping revision.
type ProgramBinding struct {
	ProgramName   string
	ProgramDigest string
}

// MappingTransformation preserves descriptor order and its resolved target.
type MappingTransformation struct {
	ProgramBinding
	TargetArtifactType string
	TargetRevision     string
	Cardinality        string
}

// ProjectionFamily names one independently verified normalized projection.
type ProjectionFamily string

const (
	ProjectionRelease       ProjectionFamily = "release"
	ProjectionArtifact      ProjectionFamily = "artifact"
	ProjectionResource      ProjectionFamily = "resource"
	ProjectionDependency    ProjectionFamily = "dependency"
	ProjectionBinding       ProjectionFamily = "binding"
	ProjectionURI           ProjectionFamily = "uri"
	ProjectionStorage       ProjectionFamily = "storage"
	ProjectionSourceMapping ProjectionFamily = "source-mapping"
)

// ProjectionCommitment binds a release projection family to authoritative bytes.
type ProjectionCommitment struct {
	SchemaVersion string
	Family        ProjectionFamily
	Commitment    []byte
}
