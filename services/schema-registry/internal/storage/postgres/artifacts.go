// Read schema artifacts and related records from a read-only snapshot of the current schema release.

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// Artifact returns an activated-reachable immutable artifact revision.
func (s *Snapshot) Artifact(ctx context.Context, revision string) (model.Artifact, error) {
	const query = `
SELECT a.artifact_revision, a.kind, a.artifact_type, a.created_under_schema_version,
       a.schema_class, a.target_schema_version, a.canonical_uri, a.payload_media_type,
       a.payload_digest, a.payload_size, a.payload_bytes, a.extensions
FROM schema_registry.artifacts AS a
JOIN schema_registry.activated_revision_reachability AS r
  ON r.artifact_revision = a.artifact_revision
WHERE a.artifact_revision = $1`
	artifact, err := scanArtifact(s.tx.QueryRow(ctx, query, revision))
	if err != nil {
		return model.Artifact{}, err
	}
	if !validBytes(artifact.Payload.Bytes, artifact.Payload.Size, artifact.Payload.Digest) {
		return model.Artifact{}, ErrInconsistentState
	}
	return artifact, nil
}

// ReleaseArtifact returns the selected artifact and verifies it belongs to an activated release.
func (s *Snapshot) ReleaseArtifact(ctx context.Context, schemaVersion, kind, artifactType string) (model.Artifact, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionArtifact); err != nil {
		return model.Artifact{}, err
	}
	const query = `
SELECT a.artifact_revision, a.kind, a.artifact_type, a.created_under_schema_version,
       a.schema_class, a.target_schema_version, a.canonical_uri, a.payload_media_type,
       a.payload_digest, a.payload_size, a.payload_bytes, a.extensions
FROM schema_registry.release_artifacts AS ra
JOIN schema_registry.releases AS rel ON rel.schema_version = ra.schema_version
JOIN schema_registry.artifacts AS a ON a.artifact_revision = ra.artifact_revision
WHERE ra.schema_version = $1 AND ra.kind = $2 AND ra.artifact_type = $3
  AND rel.activated_sequence IS NOT NULL`
	artifact, err := scanArtifact(s.tx.QueryRow(ctx, query, schemaVersion, kind, artifactType))
	if err != nil {
		return model.Artifact{}, err
	}
	if !validBytes(artifact.Payload.Bytes, artifact.Payload.Size, artifact.Payload.Digest) {
		return model.Artifact{}, ErrInconsistentState
	}
	return artifact, nil
}

// ReleaseArtifacts returns selected artifacts in the contract's canonical order.
func (s *Snapshot) ReleaseArtifacts(ctx context.Context, schemaVersion string, filter model.ArtifactFilter, afterKind, afterType *string, limit int) ([]model.Artifact, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionArtifact); err != nil {
		return nil, err
	}
	if filter.Kind == "source-mapping" {
		if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionSourceMapping); err != nil {
			return nil, err
		}
	}
	const query = `
SELECT a.artifact_revision, a.kind, a.artifact_type, a.created_under_schema_version,
       a.schema_class, a.target_schema_version, a.canonical_uri, a.payload_media_type,
       a.payload_digest, a.payload_size, NULL::bytea, a.extensions
FROM schema_registry.release_artifacts AS ra
JOIN schema_registry.releases AS rel ON rel.schema_version = ra.schema_version
JOIN schema_registry.artifacts AS a ON a.artifact_revision = ra.artifact_revision
WHERE ra.schema_version = $1 AND rel.activated_sequence IS NOT NULL
   AND ($2::text IS NULL OR (ra.kind, ra.artifact_type) > ($2, $3))
   AND ($4::text = '' OR ra.kind = $4)
   AND ($5::text = '' OR ra.artifact_type = $5)
   AND ($6::text = '' OR a.schema_class = $6)
   AND ($7::text = '' OR EXISTS (
     SELECT 1 FROM schema_registry.artifact_dependencies AS dependency
     WHERE dependency.artifact_revision = a.artifact_revision
       AND dependency.dependency_revision = $7
   ))
    AND ($8::text = '' OR EXISTS (SELECT 1 FROM schema_registry.source_mapping_details AS detail WHERE detail.schema_version = $1 AND detail.mapping_revision = a.artifact_revision AND detail.source_artifact_type = $8))
    AND ($9::text = '' OR EXISTS (SELECT 1 FROM schema_registry.source_mapping_transformations AS transformation WHERE transformation.schema_version = $1 AND transformation.mapping_revision = a.artifact_revision AND transformation.target_artifact_type = $9))
    AND ($10::text = '' OR EXISTS (SELECT 1 FROM schema_registry.source_mapping_details AS detail WHERE detail.schema_version = $1 AND detail.mapping_revision = a.artifact_revision AND detail.engine_language = $10))
    AND ($11::text = '' OR EXISTS (SELECT 1 FROM schema_registry.source_mapping_details AS detail WHERE detail.schema_version = $1 AND detail.mapping_revision = a.artifact_revision AND detail.execution_profile = $11))
ORDER BY ra.kind COLLATE "C", ra.artifact_type COLLATE "C"
LIMIT $12`
	return scanArtifacts(ctx, s.tx, query, schemaVersion, afterKind, afterType, filter.Kind, filter.ArtifactType, filter.SchemaClass, filter.DependsOnRevision, filter.SourceArtifactType, filter.TargetArtifactType, filter.EngineLanguage, filter.ExecutionProfile, limit)
}

// ArtifactHistory returns every activated selection for a logical identity.
func (s *Snapshot) ArtifactHistory(ctx context.Context, kind, artifactType string, beforeSequence *int64, maxSequence int64, limit int) ([]model.ReleaseArtifact, error) {
	const query = `
SELECT ra.schema_version, ra.kind, ra.artifact_type, ra.artifact_revision, rel.activated_sequence
FROM schema_registry.release_artifacts AS ra
JOIN schema_registry.releases AS rel ON rel.schema_version = ra.schema_version
WHERE ra.kind = $1 AND ra.artifact_type = $2 AND rel.activated_sequence IS NOT NULL
   AND ($3::bigint IS NULL OR rel.activated_sequence < $3)
   AND rel.activated_sequence <= $4
ORDER BY rel.activated_sequence DESC
LIMIT $5`
	rows, err := s.tx.Query(ctx, query, kind, artifactType, beforeSequence, maxSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list artifact history: %w", err)
	}
	defer rows.Close()
	history := make([]model.ReleaseArtifact, 0)
	for rows.Next() {
		var selection model.ReleaseArtifact
		if err := rows.Scan(&selection.SchemaVersion, &selection.Kind, &selection.ArtifactType, &selection.ArtifactRevision, &selection.ActivationSequence); err != nil {
			return nil, fmt.Errorf("scan artifact history: %w", err)
		}
		history = append(history, selection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact history: %w", err)
	}
	verified := make(map[string]struct{}, len(history))
	for _, selection := range history {
		if _, ok := verified[selection.SchemaVersion]; ok {
			continue
		}
		if err := s.VerifyProjection(ctx, selection.SchemaVersion, model.ProjectionArtifact); err != nil {
			return nil, err
		}
		verified[selection.SchemaVersion] = struct{}{}
	}
	return history, nil
}

// ReleaseArtifactMetadata reads a selected descriptor without loading its payload bytes.
func (s *Snapshot) ReleaseArtifactMetadata(ctx context.Context, version, kind, artifactType string) (model.Artifact, error) {
	if err := s.VerifyProjection(ctx, version, model.ProjectionArtifact); err != nil {
		return model.Artifact{}, err
	}
	const query = `SELECT a.artifact_revision,a.kind,a.artifact_type,a.created_under_schema_version,a.schema_class,a.target_schema_version,a.canonical_uri,a.payload_media_type,a.payload_digest,a.payload_size,NULL::bytea,a.extensions
FROM schema_registry.release_artifacts AS selection JOIN schema_registry.artifacts AS a ON a.artifact_revision = selection.artifact_revision
WHERE selection.schema_version = $1 AND selection.kind = $2 AND selection.artifact_type = $3`
	return scanArtifact(s.tx.QueryRow(ctx, query, version, kind, artifactType))
}

// Resources returns ordered named resources for an immutable artifact revision.
func (s *Snapshot) Resources(ctx context.Context, revision string) ([]model.Resource, error) {
	if err := requireReachableArtifact(ctx, s.tx, revision); err != nil {
		return nil, err
	}
	if err := s.verifyRevisionProjection(ctx, revision, model.ProjectionResource); err != nil {
		return nil, err
	}
	const query = `
SELECT resource.artifact_revision, resource.name, resource.role, resource.media_type,
       resource.digest, resource.size
FROM schema_registry.artifact_resources AS resource
JOIN schema_registry.activated_revision_reachability AS reachable
  ON reachable.artifact_revision = resource.artifact_revision
WHERE resource.artifact_revision = $1
ORDER BY resource.role COLLATE "C", resource.name COLLATE "C"`
	rows, err := s.tx.Query(ctx, query, revision)
	if err != nil {
		return nil, fmt.Errorf("list artifact resources: %w", err)
	}
	defer rows.Close()
	resources := make([]model.Resource, 0)
	for rows.Next() {
		var resource model.Resource
		if err := rows.Scan(&resource.ArtifactRevision, &resource.Name, &resource.Role, &resource.MediaType, &resource.Digest, &resource.Size); err != nil {
			return nil, fmt.Errorf("scan artifact resource: %w", err)
		}
		resources = append(resources, resource)
		if resource.Size < 0 || resource.Size > schemaartifacts.MaxArtifactBytes || !artifact.ValidDigest(resource.Digest) {
			return nil, ErrInconsistentState
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact resources: %w", err)
	}
	return resources, nil
}

// Resource returns one exact named resource for an activated-reachable revision.
func (s *Snapshot) Resource(ctx context.Context, revision, name string) (model.Resource, error) {
	if err := s.verifyRevisionProjection(ctx, revision, model.ProjectionResource); err != nil {
		return model.Resource{}, err
	}
	const query = `
SELECT resource.artifact_revision, resource.name, resource.role, resource.media_type,
       resource.digest, resource.size, resource.bytes
FROM schema_registry.artifact_resources AS resource
JOIN schema_registry.activated_revision_reachability AS reachable
  ON reachable.artifact_revision = resource.artifact_revision
WHERE resource.artifact_revision = $1 AND resource.name = $2`
	var resource model.Resource
	err := s.tx.QueryRow(ctx, query, revision, name).Scan(
		&resource.ArtifactRevision, &resource.Name, &resource.Role, &resource.MediaType,
		&resource.Digest, &resource.Size, &resource.Bytes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Resource{}, ErrNotFound
	}
	if err != nil {
		return model.Resource{}, fmt.Errorf("read artifact resource: %w", err)
	}
	if !validBytes(resource.Bytes, resource.Size, resource.Digest) {
		return model.Resource{}, ErrInconsistentState
	}
	return resource, nil
}

// Dependencies returns the exact immutable dependencies for one revision.
func (s *Snapshot) Dependencies(ctx context.Context, revision string) ([]model.Dependency, error) {
	if err := requireReachableArtifact(ctx, s.tx, revision); err != nil {
		return nil, err
	}
	if err := s.verifyRevisionProjection(ctx, revision, model.ProjectionDependency); err != nil {
		return nil, err
	}
	const query = `
SELECT dependency.artifact_revision, dependency.dependency_kind,
       dependency.dependency_artifact_type, dependency.dependency_revision
FROM schema_registry.artifact_dependencies AS dependency
JOIN schema_registry.activated_revision_reachability AS reachable
  ON reachable.artifact_revision = dependency.artifact_revision
WHERE dependency.artifact_revision = $1
ORDER BY dependency.dependency_kind COLLATE "C", dependency.dependency_artifact_type COLLATE "C", dependency.dependency_revision COLLATE "C"`
	rows, err := s.tx.Query(ctx, query, revision)
	if err != nil {
		return nil, fmt.Errorf("list artifact dependencies: %w", err)
	}
	defer rows.Close()
	dependencies := make([]model.Dependency, 0)
	for rows.Next() {
		var dependency model.Dependency
		if err := rows.Scan(&dependency.ArtifactRevision, &dependency.Kind, &dependency.ArtifactType, &dependency.Revision); err != nil {
			return nil, fmt.Errorf("scan artifact dependency: %w", err)
		}
		dependencies = append(dependencies, dependency)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact dependencies: %w", err)
	}
	return dependencies, nil
}

// ContextBindings returns deterministic selected bindings for a schema revision.
func (s *Snapshot) ContextBindings(ctx context.Context, schemaVersion, schemaRevision string) ([]model.SchemaContextBinding, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionBinding); err != nil {
		return nil, err
	}
	const query = `
SELECT binding.schema_version, binding.schema_revision, binding.context_revision
FROM schema_registry.schema_context_bindings AS binding
JOIN schema_registry.releases AS release ON release.schema_version = binding.schema_version
WHERE binding.schema_version = $1 AND binding.schema_revision = $2 AND release.activated_sequence IS NOT NULL
ORDER BY context_revision COLLATE "C"`
	rows, err := s.tx.Query(ctx, query, schemaVersion, schemaRevision)
	if err != nil {
		return nil, fmt.Errorf("list context bindings: %w", err)
	}
	defer rows.Close()
	bindings := make([]model.SchemaContextBinding, 0)
	for rows.Next() {
		var binding model.SchemaContextBinding
		if err := rows.Scan(&binding.SchemaVersion, &binding.SchemaRevision, &binding.ContextRevision); err != nil {
			return nil, fmt.Errorf("scan context binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate context bindings: %w", err)
	}
	return bindings, nil
}

// URIEntry returns one exact release-scoped URI catalog mapping.
func (s *Snapshot) URIEntry(ctx context.Context, schemaVersion, uri string) (model.URIEntry, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionURI); err != nil {
		return model.URIEntry{}, err
	}
	const query = `
SELECT entry.schema_version, entry.uri, entry.artifact_revision, entry.resource_name
FROM schema_registry.uri_catalog_entries AS entry
JOIN schema_registry.releases AS rel ON rel.schema_version = entry.schema_version
WHERE entry.schema_version = $1 AND entry.uri = $2 AND rel.activated_sequence IS NOT NULL`
	var entry model.URIEntry
	err := s.tx.QueryRow(ctx, query, schemaVersion, uri).Scan(
		&entry.SchemaVersion, &entry.URI, &entry.ArtifactRevision, &entry.ResourceName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.URIEntry{}, ErrNotFound
	}
	if err != nil {
		return model.URIEntry{}, fmt.Errorf("read URI catalog entry: %w", err)
	}
	return entry, nil
}

// StorageEntries returns sorted logical stores selected for one schema type.
func (s *Snapshot) StorageEntries(ctx context.Context, schemaVersion, artifactType string) ([]model.StorageEntry, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionStorage); err != nil {
		return nil, err
	}
	const query = `
SELECT entry.schema_version, entry.artifact_type, entry.store
FROM schema_registry.storage_catalog_entries AS entry
JOIN schema_registry.releases AS release ON release.schema_version = entry.schema_version
WHERE entry.schema_version = $1 AND entry.artifact_type = $2 AND release.activated_sequence IS NOT NULL
ORDER BY store COLLATE "C"`
	rows, err := s.tx.Query(ctx, query, schemaVersion, artifactType)
	if err != nil {
		return nil, fmt.Errorf("list storage entries: %w", err)
	}
	defer rows.Close()
	entries := make([]model.StorageEntry, 0)
	for rows.Next() {
		var entry model.StorageEntry
		if err := rows.Scan(&entry.SchemaVersion, &entry.ArtifactType, &entry.Store); err != nil {
			return nil, fmt.Errorf("scan storage entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage entries: %w", err)
	}
	return entries, nil
}

// SourceMapping resolves a mapping by its own logical artifact type.
func (s *Snapshot) SourceMapping(ctx context.Context, schemaVersion, mappingType string) (model.SourceMappingDetail, error) {
	item, err := s.ReleaseArtifact(ctx, schemaVersion, "source-mapping", mappingType)
	if err != nil {
		return model.SourceMappingDetail{}, err
	}
	return s.SourceMappingByRevision(ctx, schemaVersion, item.Revision)
}

// SourceMappingByRevision returns resolved details for one selected mapping revision.
func (s *Snapshot) SourceMappingByRevision(ctx context.Context, schemaVersion, revision string) (model.SourceMappingDetail, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionSourceMapping); err != nil {
		return model.SourceMappingDetail{}, err
	}
	const query = `
	SELECT detail.schema_version, detail.mapping_revision, detail.source_artifact_type, detail.source_revision,
	       detail.target_schema_version, detail.engine_language, detail.engine_language_version, detail.execution_profile,
	       coalesce(detail.condition_program_name, ''), coalesce(detail.condition_program_digest, '')
FROM schema_registry.source_mapping_details AS detail
JOIN schema_registry.releases AS release ON release.schema_version = detail.schema_version
WHERE detail.schema_version = $1 AND detail.mapping_revision = $2 AND release.activated_sequence IS NOT NULL`
	var detail model.SourceMappingDetail
	var condition model.ProgramBinding
	err := s.tx.QueryRow(ctx, query, schemaVersion, revision).Scan(
		&detail.SchemaVersion, &detail.MappingRevision, &detail.SourceArtifactType, &detail.SourceRevision,
		&detail.TargetSchemaVersion, &detail.EngineLanguage, &detail.EngineLanguageVersion, &detail.ExecutionProfile,
		&condition.ProgramName, &condition.ProgramDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SourceMappingDetail{}, ErrNotFound
	}
	if err != nil {
		return model.SourceMappingDetail{}, fmt.Errorf("read source mapping revision: %w", err)
	}
	if condition.ProgramName != "" {
		detail.Condition = &condition
	}
	rows, err := s.tx.Query(ctx, `SELECT ordinal, target_artifact_type, target_revision, cardinality, program_name, program_digest FROM schema_registry.source_mapping_transformations WHERE schema_version = $1 AND mapping_revision = $2 ORDER BY ordinal`, schemaVersion, revision)
	if err != nil {
		return model.SourceMappingDetail{}, fmt.Errorf("read mapping transformations: %w", err)
	}
	defer rows.Close()
	detail.Transformations = make([]model.SourceMappingTransformation, 0)
	for rows.Next() {
		var ordinal int
		var transformation model.SourceMappingTransformation
		if err := rows.Scan(&ordinal, &transformation.TargetArtifactType, &transformation.TargetRevision, &transformation.Cardinality, &transformation.ProgramName, &transformation.ProgramDigest); err != nil {
			return model.SourceMappingDetail{}, fmt.Errorf("scan mapping transformation: %w", err)
		}
		if ordinal != len(detail.Transformations) {
			// Transformation order is semantic; gaps or duplicate ordinals invalidate the entire mapping.
			return model.SourceMappingDetail{}, ErrInconsistentState
		}
		detail.Transformations = append(detail.Transformations, transformation)
	}
	if err := rows.Err(); err != nil {
		return model.SourceMappingDetail{}, fmt.Errorf("iterate mapping transformations: %w", err)
	}
	if len(detail.Transformations) == 0 {
		return model.SourceMappingDetail{}, ErrInconsistentState
	}
	return detail, nil
}

// requireReachableArtifact returns ErrNotFound unless an activated release makes the revision visible.
func requireReachableArtifact(ctx context.Context, tx pgx.Tx, revision string) error {
	var reachable bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_registry.activated_revision_reachability WHERE artifact_revision = $1)`, revision).Scan(&reachable)
	if err != nil {
		return fmt.Errorf("check artifact reachability: %w", err)
	}
	if !reachable {
		return ErrNotFound
	}
	return nil
}

// scanArtifact decodes artifact metadata and optional content, mapping absent rows to ErrNotFound.
func scanArtifact(row pgx.Row) (model.Artifact, error) {
	var artifact model.Artifact
	err := row.Scan(
		&artifact.Revision, &artifact.Kind, &artifact.Type, &artifact.CreatedUnderSchemaVersion,
		&artifact.SchemaClass, &artifact.TargetSchemaVersion, &artifact.CanonicalURI, &artifact.Payload.MediaType,
		&artifact.Payload.Digest, &artifact.Payload.Size, &artifact.Payload.Bytes, &artifact.Extensions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Artifact{}, ErrNotFound
	}
	if err != nil {
		return model.Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}
	return artifact, nil
}

// scanArtifacts collects artifact rows and propagates query, scan, and iteration failures without returning a partial list.
func scanArtifacts(ctx context.Context, tx pgx.Tx, query string, arguments ...any) ([]model.Artifact, error) {
	rows, err := tx.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()
	artifacts := make([]model.Artifact, 0)
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		if artifact.Payload.Bytes != nil && !validBytes(artifact.Payload.Bytes, artifact.Payload.Size, artifact.Payload.Digest) {
			return nil, ErrInconsistentState
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return artifacts, nil
}
