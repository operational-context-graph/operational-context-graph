// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package model defines immutable records shared between storage and services.
package model

import "time"

// Release is an immutable imported release. Activation makes it API-visible.
type Release struct {
	SchemaVersion          string
	ManifestDigest         string
	ManifestBytes          []byte
	PreviousSchemaVersion  *string
	PreviousManifestDigest *string
	SourceRepository       string
	SourceCommit           string
	Changes                []byte
	ActivationSequence     *int64
	ProjectionCommitment   []byte
}

// CurrentState is the current activated release pointer from one read snapshot.
type CurrentState struct {
	SchemaVersion string
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
	SchemaVersion    string
	Kind             string
	ArtifactType     string
	ArtifactRevision string
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
	TargetArtifactType    string
	TargetRevision        string
	TargetSchemaVersion   string
	Cardinality           string
	EngineLanguage        string
	EngineLanguageVersion string
	ExecutionProfile      string
	ProgramName           string
	ProgramDigest         string
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

// Activation describes the test-only future-writer activation input.
type Activation struct {
	CandidateVersion string
	ExpectedPrevious *string
	At               time.Time
}
