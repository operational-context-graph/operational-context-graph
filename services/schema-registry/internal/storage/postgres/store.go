// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/source-mapping"
)

var (
	// ErrNoActiveRelease means the snapshot has no current activated release.
	ErrNoActiveRelease = errors.New("no active release")
	// ErrNotFound means a requested visible storage record is absent.
	ErrNotFound = errors.New("registry record not found")
	// ErrInconsistentState means authoritative and projected state disagree.
	ErrInconsistentState = errors.New("inconsistent registry state")
)

// Timeouts bound pool acquisition and every database operation in a read snapshot.
type Timeouts struct {
	Acquire           time.Duration
	Query             time.Duration
	Statement         time.Duration
	Lock              time.Duration
	IdleInTransaction time.Duration
}

// Store owns static, fully-qualified read queries. It never exposes a writer.
type Store struct {
	pool     *pgxpool.Pool
	timeouts Timeouts
}

// NewStore creates the read-only storage boundary for a configured pool.
func NewStore(pool *pgxpool.Pool, timeouts Timeouts) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	if err := timeouts.validate(); err != nil {
		return nil, err
	}
	return &Store{pool: pool, timeouts: timeouts}, nil
}

func (t Timeouts) validate() error {
	if t.Acquire <= 0 || t.Query <= 0 || t.Statement <= 0 || t.Lock <= 0 || t.IdleInTransaction <= 0 {
		return errors.New("all database timeouts must be positive")
	}
	return nil
}

// Read executes fn in one read-only repeatable-read snapshot. It acquires and
// releases its connection before the caller can write a network response.
func (s *Store) Read(ctx context.Context, fn func(context.Context, *Snapshot) error) error {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, s.timeouts.Acquire)
	defer cancelAcquire()
	conn, err := s.pool.Acquire(acquireCtx)
	if err != nil {
		return fmt.Errorf("acquire read connection: %w", err)
	}
	defer conn.Release()

	operationCtx, cancelOperation := context.WithTimeout(acquireCtx, s.timeouts.Query)
	defer cancelOperation()
	tx, err := conn.BeginTx(operationCtx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), s.timeouts.Query)
			defer cancelCleanup()
			_ = tx.Rollback(cleanupCtx)
		}
	}()

	queryTimeout := s.timeouts.Query
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		queryTimeout = min(queryTimeout, remaining)
	}
	queryCtx := operationCtx
	if err := setLocalTimeouts(queryCtx, tx, s.timeouts.capped(queryTimeout)); err != nil {
		return err
	}
	current, err := readCurrentState(queryCtx, tx)
	if err != nil {
		return err
	}
	snapshot := &Snapshot{tx: tx, Current: current}
	if err := fn(queryCtx, snapshot); err != nil {
		return err
	}
	if err := tx.Commit(queryCtx); err != nil {
		return fmt.Errorf("commit read transaction: %w", err)
	}
	completed = true
	return nil
}

func (t Timeouts) capped(limit time.Duration) Timeouts {
	t.Statement = min(t.Statement, limit)
	t.Lock = min(t.Lock, limit)
	t.IdleInTransaction = min(t.IdleInTransaction, limit)
	return t
}

func setLocalTimeouts(ctx context.Context, tx pgx.Tx, timeouts Timeouts) error {
	values := []struct {
		name  string
		value time.Duration
	}{
		{"statement_timeout", timeouts.Statement},
		{"lock_timeout", timeouts.Lock},
		{"idle_in_transaction_session_timeout", timeouts.IdleInTransaction},
	}
	for _, setting := range values {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config($1, $2, true)", setting.name, strconv.FormatInt(setting.value.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("set local %s: %w", setting.name, err)
		}
	}
	return nil
}

func readCurrentState(ctx context.Context, tx pgx.Tx) (model.CurrentState, error) {
	var schemaVersion *string
	var activationSequence *int64
	var snapshotSequence int64
	err := tx.QueryRow(ctx, `
SELECT state.current_schema_version, release.activated_sequence,
       COALESCE((SELECT MAX(activated_sequence) FROM schema_registry.releases), 0)
FROM schema_registry.registry_state AS state
LEFT JOIN schema_registry.releases AS release
  ON release.schema_version = state.current_schema_version
WHERE state.singleton = true`).Scan(&schemaVersion, &activationSequence, &snapshotSequence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.CurrentState{}, ErrInconsistentState
		}
		return model.CurrentState{}, fmt.Errorf("read current registry state: %w", err)
	}
	if schemaVersion == nil {
		return model.CurrentState{}, ErrNoActiveRelease
	}
	if activationSequence == nil || *activationSequence <= 0 {
		return model.CurrentState{}, ErrInconsistentState
	}
	return model.CurrentState{SchemaVersion: *schemaVersion, ActivationSequence: *activationSequence, SnapshotSequence: snapshotSequence}, nil
}

// Snapshot exposes repository queries confined to one transaction.
type Snapshot struct {
	tx      pgx.Tx
	Current model.CurrentState
}

// CurrentState returns the pointer and immutable paging ceiling for this read snapshot.
func (s *Snapshot) CurrentState() model.CurrentState { return s.Current }

// CurrentRelease returns the release named by this snapshot's current pointer.
func (s *Snapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	return s.Release(ctx, s.Current.SchemaVersion)
}

// Release returns one activated release only.
func (s *Snapshot) Release(ctx context.Context, schemaVersion string) (model.Release, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionRelease); err != nil {
		return model.Release{}, err
	}
	const query = `
SELECT schema_version, manifest_digest, manifest_bytes, previous_schema_version,
	     previous_manifest_digest, source_repository, source_commit, changes,
	     projection_commitment, activated_sequence
FROM schema_registry.releases
WHERE schema_version = $1 AND activated_sequence IS NOT NULL`
	release, err := scanRelease(s.tx.QueryRow(ctx, query, schemaVersion))
	if err != nil {
		return model.Release{}, err
	}
	if manifestDigest(release.ManifestBytes) != release.ManifestDigest {
		return model.Release{}, ErrInconsistentState
	}
	changes, err := manifestChanges(release.ManifestBytes)
	if err != nil {
		return model.Release{}, err
	}
	release.Changes = changes
	return release, nil
}

// Releases returns activated releases in immutable activation order.
func (s *Snapshot) Releases(ctx context.Context, beforeSequence *int64, limit int) ([]model.Release, error) {
	return s.ReleasesAt(ctx, beforeSequence, s.Current.SnapshotSequence, limit)
}

// ReleasesAt returns activated releases within a cursor-bound read snapshot.
func (s *Snapshot) ReleasesAt(ctx context.Context, beforeSequence *int64, maxSequence int64, limit int) ([]model.Release, error) {
	if err := s.validateReleaseChain(ctx, maxSequence); err != nil {
		return nil, err
	}
	const query = `
SELECT schema_version, manifest_digest, manifest_bytes, previous_schema_version,
       previous_manifest_digest, source_repository, source_commit, changes,
       activated_sequence, projection_commitment
FROM schema_registry.releases
WHERE activated_sequence IS NOT NULL
   AND ($1::bigint IS NULL OR activated_sequence < $1)
	  AND activated_sequence <= $2
ORDER BY activated_sequence DESC
LIMIT $3`
	rows, err := s.tx.Query(ctx, query, beforeSequence, maxSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list activated releases: %w", err)
	}
	defer rows.Close()
	var releaseRows []releaseRow
	for rows.Next() {
		release, err := scanReleaseRow(rows)
		if err != nil {
			return nil, err
		}
		if manifestDigest(release.release.ManifestBytes) != release.release.ManifestDigest {
			return nil, ErrInconsistentState
		}
		releaseRows = append(releaseRows, release)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activated releases: %w", err)
	}
	releases := make([]model.Release, len(releaseRows))
	for index, release := range releaseRows {
		if err := s.VerifyProjection(ctx, release.release.SchemaVersion, model.ProjectionRelease); err != nil {
			return nil, err
		}
		changes, err := manifestChanges(release.release.ManifestBytes)
		if err != nil {
			return nil, err
		}
		release.release.Changes = changes
		releases[index] = release.release
	}
	return releases, nil
}

// IsAncestor verifies immutable same-chain ancestry using indexed endpoint
// lineage evidence. The importer owns the evidence and the API never changes
// it or walks the intervening predecessor history.
func (s *Snapshot) IsAncestor(ctx context.Context, ancestorVersion, descendantVersion string) (bool, error) {
	const query = `
WITH endpoints AS (
  SELECT
    ancestor.chain_id AS ancestor_chain_id,
    ancestor.chain_position AS ancestor_position,
    ancestor.predecessor_schema_version AS ancestor_predecessor_version,
    ancestor.predecessor_manifest_digest AS ancestor_predecessor_digest,
    ancestor_release.previous_schema_version AS ancestor_release_predecessor_version,
    ancestor_release.previous_manifest_digest AS ancestor_release_predecessor_digest,
    ancestor_predecessor.manifest_digest AS ancestor_predecessor_actual_digest,
    ancestor_predecessor_lineage.chain_id AS ancestor_predecessor_chain_id,
    ancestor_predecessor_lineage.chain_position AS ancestor_predecessor_position,
    descendant.chain_id AS descendant_chain_id,
    descendant.chain_position AS descendant_position,
    descendant.predecessor_schema_version AS descendant_predecessor_version,
    descendant.predecessor_manifest_digest AS descendant_predecessor_digest,
    descendant_release.previous_schema_version AS descendant_release_predecessor_version,
    descendant_release.previous_manifest_digest AS descendant_release_predecessor_digest,
    descendant_predecessor.manifest_digest AS descendant_predecessor_actual_digest,
    descendant_predecessor_lineage.chain_id AS descendant_predecessor_chain_id,
    descendant_predecessor_lineage.chain_position AS descendant_predecessor_position
  FROM schema_registry.release_lineage AS ancestor
  JOIN schema_registry.releases AS ancestor_release
    ON ancestor_release.schema_version = ancestor.schema_version
  JOIN schema_registry.release_lineage AS descendant
    ON descendant.schema_version = $2
  JOIN schema_registry.releases AS descendant_release
    ON descendant_release.schema_version = descendant.schema_version
  LEFT JOIN schema_registry.releases AS ancestor_predecessor
    ON ancestor_predecessor.schema_version = ancestor.predecessor_schema_version
  LEFT JOIN schema_registry.release_lineage AS ancestor_predecessor_lineage
    ON ancestor_predecessor_lineage.schema_version = ancestor.predecessor_schema_version
  LEFT JOIN schema_registry.releases AS descendant_predecessor
    ON descendant_predecessor.schema_version = descendant.predecessor_schema_version
  LEFT JOIN schema_registry.release_lineage AS descendant_predecessor_lineage
    ON descendant_predecessor_lineage.schema_version = descendant.predecessor_schema_version
  WHERE ancestor.schema_version = $1
), validated AS (
  SELECT *,
    COALESCE((ancestor_predecessor_version, ancestor_predecessor_digest)
          IS NOT DISTINCT FROM (ancestor_release_predecessor_version, ancestor_release_predecessor_digest)
      AND (descendant_predecessor_version, descendant_predecessor_digest)
          IS NOT DISTINCT FROM (descendant_release_predecessor_version, descendant_release_predecessor_digest)
      AND (
        (ancestor_position = 1 AND ancestor_predecessor_version IS NULL) OR
        (ancestor_position > 1 AND ancestor_predecessor_version IS NOT NULL AND
         ancestor_predecessor_actual_digest = ancestor_predecessor_digest AND
         ancestor_predecessor_chain_id = ancestor_chain_id AND
         ancestor_predecessor_position = ancestor_position - 1)
      )
      AND (
        (descendant_position = 1 AND descendant_predecessor_version IS NULL) OR
        (descendant_position > 1 AND descendant_predecessor_version IS NOT NULL AND
         descendant_predecessor_actual_digest = descendant_predecessor_digest AND
         descendant_predecessor_chain_id = descendant_chain_id AND
         descendant_predecessor_position = descendant_position - 1)
      ), false) AS valid,
    ancestor_chain_id = descendant_chain_id
      AND ancestor_position < descendant_position
      AS is_ancestor
  FROM endpoints
)

SELECT valid, is_ancestor FROM validated`
	var valid bool
	var isAncestor bool
	if err := s.tx.QueryRow(ctx, query, ancestorVersion, descendantVersion).Scan(&valid, &isAncestor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrInconsistentState
		}
		return false, fmt.Errorf("verify release ancestry: %w", err)
	}
	if !valid {
		return false, ErrInconsistentState
	}
	return isAncestor, nil
}

// validateReleaseChain checks every activated row through the requested
// snapshot ceiling. It validates sequence continuity and predecessor identity
// without materializing the complete history in the API process.
func (s *Snapshot) validateReleaseChain(ctx context.Context, maxSequence int64) error {
	const query = `
WITH ordered AS (
  SELECT activated_sequence, schema_version, manifest_digest,
         previous_schema_version, previous_manifest_digest,
         LEAD(activated_sequence) OVER (ORDER BY activated_sequence DESC) AS predecessor_sequence,
         LEAD(schema_version) OVER (ORDER BY activated_sequence DESC) AS predecessor_version,
         LEAD(manifest_digest) OVER (ORDER BY activated_sequence DESC) AS predecessor_digest
  FROM schema_registry.releases
  WHERE activated_sequence IS NOT NULL AND activated_sequence <= $1
)
SELECT COUNT(*), COALESCE(BOOL_OR(
  activated_sequence <= 0 OR
  (predecessor_sequence IS NULL AND (
    activated_sequence <> 1 OR previous_schema_version IS NOT NULL OR previous_manifest_digest IS NOT NULL
  )) OR
  (predecessor_sequence IS NOT NULL AND (
    predecessor_sequence <> activated_sequence - 1 OR
    previous_schema_version IS NULL OR previous_manifest_digest IS NULL OR
    previous_schema_version <> predecessor_version OR previous_manifest_digest <> predecessor_digest
  ))
), false)
FROM ordered`
	var count int64
	var invalid bool
	if err := s.tx.QueryRow(ctx, query, maxSequence).Scan(&count, &invalid); err != nil {
		return fmt.Errorf("validate activation chain: %w", err)
	}
	if count == 0 || invalid {
		return ErrInconsistentState
	}
	return nil
}

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
func (s *Snapshot) ReleaseArtifacts(ctx context.Context, schemaVersion string, afterKind, afterType *string, limit int) ([]model.Artifact, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionArtifact); err != nil {
		return nil, err
	}
	const query = `
SELECT a.artifact_revision, a.kind, a.artifact_type, a.created_under_schema_version,
       a.schema_class, a.target_schema_version, a.canonical_uri, a.payload_media_type,
       a.payload_digest, a.payload_size, a.payload_bytes, a.extensions
FROM schema_registry.release_artifacts AS ra
JOIN schema_registry.releases AS rel ON rel.schema_version = ra.schema_version
JOIN schema_registry.artifacts AS a ON a.artifact_revision = ra.artifact_revision
WHERE ra.schema_version = $1 AND rel.activated_sequence IS NOT NULL
  AND ($2::text IS NULL OR (ra.kind, ra.artifact_type) > ($2, $3))
ORDER BY ra.kind COLLATE "C", ra.artifact_type COLLATE "C"
LIMIT $4`
	return scanArtifacts(ctx, s.tx, query, schemaVersion, afterKind, afterType, limit)
}

// ArtifactHistory returns every activated selection for a logical identity.
func (s *Snapshot) ArtifactHistory(ctx context.Context, kind, artifactType string, beforeSequence *int64, limit int) ([]model.ReleaseArtifact, error) {
	const query = `
SELECT ra.schema_version, ra.kind, ra.artifact_type, ra.artifact_revision
FROM schema_registry.release_artifacts AS ra
JOIN schema_registry.releases AS rel ON rel.schema_version = ra.schema_version
WHERE ra.kind = $1 AND ra.artifact_type = $2 AND rel.activated_sequence IS NOT NULL
  AND ($3::bigint IS NULL OR rel.activated_sequence < $3)
ORDER BY rel.activated_sequence DESC
LIMIT $4`
	rows, err := s.tx.Query(ctx, query, kind, artifactType, beforeSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list artifact history: %w", err)
	}
	defer rows.Close()
	history := make([]model.ReleaseArtifact, 0)
	for rows.Next() {
		var selection model.ReleaseArtifact
		if err := rows.Scan(&selection.SchemaVersion, &selection.Kind, &selection.ArtifactType, &selection.ArtifactRevision); err != nil {
			return nil, fmt.Errorf("scan artifact history: %w", err)
		}
		history = append(history, selection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact history: %w", err)
	}
	return history, nil
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
       resource.digest, resource.size, resource.bytes
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
		if err := rows.Scan(&resource.ArtifactRevision, &resource.Name, &resource.Role, &resource.MediaType, &resource.Digest, &resource.Size, &resource.Bytes); err != nil {
			return nil, fmt.Errorf("scan artifact resource: %w", err)
		}
		resources = append(resources, resource)
		if !validBytes(resource.Bytes, resource.Size, resource.Digest) {
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

func (s *Snapshot) verifyRevisionProjection(ctx context.Context, revision string, family model.ProjectionFamily) error {
	rows, err := s.tx.Query(ctx, `
SELECT DISTINCT selection.schema_version
FROM schema_registry.release_artifacts AS selection
JOIN schema_registry.releases AS release ON release.schema_version = selection.schema_version
WHERE selection.artifact_revision = $1 AND release.activated_sequence IS NOT NULL`, revision)
	if err != nil {
		return fmt.Errorf("find artifact release selections: %w", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var schemaVersion string
		if err := rows.Scan(&schemaVersion); err != nil {
			return fmt.Errorf("scan artifact release selection: %w", err)
		}
		found = true
		if err := s.VerifyProjection(ctx, schemaVersion, family); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate artifact release selections: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// ContextBindings returns deterministic selected bindings for a schema revision.
func (s *Snapshot) ContextBindings(ctx context.Context, schemaVersion, schemaRevision string) ([]model.ContextBinding, error) {
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
	bindings := make([]model.ContextBinding, 0)
	for rows.Next() {
		var binding model.ContextBinding
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

// SourceMapping returns the single resolved mapping for a selected source schema.
func (s *Snapshot) SourceMapping(ctx context.Context, schemaVersion, sourceArtifactType string) (model.SourceMappingDetail, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionSourceMapping); err != nil {
		return model.SourceMappingDetail{}, err
	}
	const query = `
SELECT schema_version, mapping_revision, source_artifact_type, source_revision,
       target_artifact_type, target_revision, target_schema_version, cardinality,
       engine_language, engine_language_version, execution_profile, program_name, program_digest
FROM schema_registry.source_mapping_details
JOIN schema_registry.releases AS release ON release.schema_version = source_mapping_details.schema_version
WHERE source_mapping_details.schema_version = $1 AND source_mapping_details.source_artifact_type = $2 AND release.activated_sequence IS NOT NULL`
	var detail model.SourceMappingDetail
	err := s.tx.QueryRow(ctx, query, schemaVersion, sourceArtifactType).Scan(
		&detail.SchemaVersion, &detail.MappingRevision, &detail.SourceArtifactType, &detail.SourceRevision,
		&detail.TargetArtifactType, &detail.TargetRevision, &detail.TargetSchemaVersion, &detail.Cardinality,
		&detail.EngineLanguage, &detail.EngineLanguageVersion, &detail.ExecutionProfile, &detail.ProgramName, &detail.ProgramDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SourceMappingDetail{}, ErrNotFound
	}
	if err != nil {
		return model.SourceMappingDetail{}, fmt.Errorf("read source mapping: %w", err)
	}
	return detail, nil
}

// VerifyCommitment compares an authoritative computed commitment with storage.
func (s *Snapshot) VerifyCommitment(ctx context.Context, schemaVersion string, family model.ProjectionFamily, expected []byte) error {
	const query = `
SELECT commitment
FROM schema_registry.release_projection_commitments
WHERE schema_version = $1 AND family = $2`
	var actual []byte
	if err := s.tx.QueryRow(ctx, query, schemaVersion, family).Scan(&actual); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInconsistentState
		}
		return fmt.Errorf("read projection commitment: %w", err)
	}
	if !equalBytes(actual, expected) {
		return ErrInconsistentState
	}
	return nil
}

// VerifyProjection recomputes one authoritative projection commitment from the
// indexed rows needed by a response. Missing, extra, and altered rows differ.
func (s *Snapshot) VerifyProjection(ctx context.Context, schemaVersion string, family model.ProjectionFamily) error {
	if err := requireActiveRelease(ctx, s.tx, schemaVersion); err != nil {
		return err
	}
	actual, err := projectionRows(ctx, s.tx, schemaVersion, family)
	if err != nil {
		return err
	}
	manifestBytes, err := releaseManifestBytes(ctx, s.tx, schemaVersion)
	if err != nil {
		return err
	}
	parsed, err := manifest.Parse(manifestBytes)
	if err != nil {
		return fmt.Errorf("parse authoritative manifest: %w", ErrInconsistentState)
	}
	expected, err := manifestProjectionRows(parsed, manifestBytes, family)
	if err != nil {
		if family != model.ProjectionSourceMapping {
			return err
		}
		expected, err = sourceMappingProjectionRows(ctx, s.tx, parsed)
		if err != nil {
			return err
		}
	}
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("%w: %s projection differs", ErrInconsistentState, family)
	}
	return s.VerifyCommitment(ctx, schemaVersion, family, ProjectionCommitment(expected))
}

func sourceMappingProjectionRows(ctx context.Context, tx pgx.Tx, value manifest.Manifest) ([]string, error) {
	rows := make([]string, 0)
	for _, item := range value.Artifacts {
		if item.Kind != "source-mapping" {
			continue
		}
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT payload_bytes FROM schema_registry.artifacts WHERE artifact_revision = $1`, item.Revision).Scan(&payload); err != nil {
			return nil, fmt.Errorf("read source-mapping descriptor: %w", ErrInconsistentState)
		}
		if manifestDigest(payload) != item.Payload.Digest || int64(len(payload)) != item.Payload.Size {
			return nil, ErrInconsistentState
		}
		descriptor, err := sourcemapping.Parse(payload)
		if err != nil {
			return nil, fmt.Errorf("parse source-mapping descriptor: %w", ErrInconsistentState)
		}
		sourceRevision, targetRevision, foundProgram := "", "", false
		for _, dependency := range item.Dependencies {
			if dependency.Kind != "schema" {
				continue
			}
			if dependency.Type == descriptor.SourceSchema.ID {
				sourceRevision = dependency.Revision
			}
			if dependency.Type == descriptor.TargetSchema.ID {
				targetRevision = dependency.Revision
			}
		}
		for _, resource := range item.Resources {
			if resource.Name == descriptor.ProgramRef && resource.Digest == descriptor.ProgramDigest {
				foundProgram = true
			}
		}
		if sourceRevision == "" || targetRevision == "" || !foundProgram {
			return nil, ErrInconsistentState
		}
		rows = append(rows, projectionRow(item.Revision, descriptor.SourceSchema.ID, sourceRevision, descriptor.TargetSchema.ID, targetRevision, item.TargetSchema, descriptor.Cardinality, descriptor.Engine.Language, descriptor.Engine.LanguageVersion, descriptor.Engine.ExecutionProfile, descriptor.ProgramRef, descriptor.ProgramDigest))
	}
	sort.Strings(rows)
	return rows, nil
}

func releaseManifestBytes(ctx context.Context, tx pgx.Tx, schemaVersion string) ([]byte, error) {
	var bytes []byte
	err := tx.QueryRow(ctx, `SELECT manifest_bytes FROM schema_registry.releases WHERE schema_version = $1 AND activated_sequence IS NOT NULL`, schemaVersion).Scan(&bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read authoritative manifest: %w", err)
	}
	return bytes, nil
}

func manifestProjectionRows(value manifest.Manifest, bytes []byte, family model.ProjectionFamily) ([]string, error) {
	rows := make([]string, 0)
	switch family {
	case model.ProjectionRelease:
		previousVersion, previousDigest := "", ""
		if value.PreviousRelease != nil {
			previousVersion, previousDigest = value.PreviousRelease.SchemaVersion, value.PreviousRelease.ManifestDigest
		}
		return []string{projectionRow(value.SchemaVersion, manifestDigest(bytes), previousVersion, previousDigest, value.Source.Repository, value.Source.Commit)}, nil
	case model.ProjectionArtifact:
		for _, item := range value.Artifacts {
			rows = append(rows, projectionRow(string(item.Kind), item.Type, item.Revision, item.CreatedUnderSchemaVersion, string(item.SchemaClass), item.TargetSchema, item.CanonicalURI, item.Payload.MediaType, item.Payload.Digest, strconv.FormatInt(item.Payload.Size, 10), canonicalJSON(item.Extensions)))
		}
	case model.ProjectionResource:
		for _, item := range value.Artifacts {
			for _, resource := range item.Resources {
				rows = append(rows, projectionRow(item.Revision, resource.Name, resource.Role, resource.MediaType, resource.Digest, strconv.FormatInt(resource.Size, 10)))
			}
		}
	case model.ProjectionDependency:
		for _, item := range value.Artifacts {
			for _, dependency := range item.Dependencies {
				rows = append(rows, projectionRow(item.Revision, string(dependency.Kind), dependency.Type, dependency.Revision))
			}
		}
	case model.ProjectionBinding:
		for _, binding := range value.SchemaContextBindings {
			for _, contextRevision := range binding.ContextRevisions {
				rows = append(rows, projectionRow(binding.SchemaRevision, contextRevision))
			}
		}
	case model.ProjectionURI:
		for _, entry := range value.URICatalog {
			rows = append(rows, projectionRow(entry.URI, entry.ArtifactRevision, entry.Resource))
		}
	case model.ProjectionStorage:
		for _, entry := range value.StorageCatalog {
			for _, store := range entry.Stores {
				rows = append(rows, projectionRow(entry.ArtifactType, store))
			}
		}
	case model.ProjectionSourceMapping:
		for _, item := range value.Artifacts {
			if item.Kind != "source-mapping" {
				continue
			}
			// The manifest does not embed descriptor fields. The importer validates
			// this projection from the descriptor bytes before it stores the row.
			return nil, ErrInconsistentState
		}
	default:
		return nil, fmt.Errorf("unknown projection family %q", family)
	}
	sort.Strings(rows)
	return rows, nil
}

func manifestDigest(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return "sha-256:" + hex.EncodeToString(sum[:])
}

// manifestChanges returns the authoritative stored manifest change summary.
// Callers invoke it only after manifest validation succeeds.
func manifestChanges(bytes []byte) ([]byte, error) {
	var document struct {
		Changes json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(bytes, &document); err != nil {
		return nil, fmt.Errorf("read manifest changes: %w", ErrInconsistentState)
	}
	if len(document.Changes) == 0 {
		return nil, nil
	}
	return append([]byte(nil), document.Changes...), nil
}

func projectionRow(fields ...string) string {
	encoded := make([]string, len(fields))
	for index, field := range fields {
		encoded[index] = hex.EncodeToString([]byte(field))
	}
	return strings.Join(encoded, ":")
}

func canonicalJSON(value any) string {
	if value == nil || (reflect.ValueOf(value).Kind() == reflect.Map && reflect.ValueOf(value).IsNil()) {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func validBytes(bytes []byte, size int64, digest string) bool {
	if int64(len(bytes)) != size || size < 0 || size > 16*1024*1024 {
		return false
	}
	return manifestDigest(bytes) == digest
}

func requireActiveRelease(ctx context.Context, tx pgx.Tx, schemaVersion string) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_registry.releases WHERE schema_version = $1 AND activated_sequence IS NOT NULL)`, schemaVersion).Scan(&active)
	if err != nil {
		return fmt.Errorf("check active release: %w", err)
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

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

func projectionRows(ctx context.Context, tx pgx.Tx, schemaVersion string, family model.ProjectionFamily) ([]string, error) {
	query, err := projectionQuery(family)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, query, schemaVersion)
	if err != nil {
		return nil, fmt.Errorf("read %s projection: %w", family, err)
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan %s projection: %w", family, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s projection: %w", family, err)
	}
	return values, nil
}

func projectionQuery(family model.ProjectionFamily) (string, error) {
	switch family {
	case model.ProjectionRelease:
		return `SELECT array_to_string(ARRAY[encode(convert_to(schema_version, 'UTF8'), 'hex'),encode(convert_to(manifest_digest, 'UTF8'), 'hex'),encode(convert_to(coalesce(previous_schema_version, ''), 'UTF8'), 'hex'),encode(convert_to(coalesce(previous_manifest_digest, ''), 'UTF8'), 'hex'),encode(convert_to(source_repository, 'UTF8'), 'hex'),encode(convert_to(source_commit, 'UTF8'), 'hex')], ':') FROM schema_registry.releases WHERE schema_version = $1`, nil
	case model.ProjectionArtifact:
		return `SELECT array_to_string(ARRAY[encode(convert_to(ra.kind, 'UTF8'), 'hex'),encode(convert_to(ra.artifact_type, 'UTF8'), 'hex'),encode(convert_to(a.artifact_revision, 'UTF8'), 'hex'),encode(convert_to(a.created_under_schema_version, 'UTF8'), 'hex'),encode(convert_to(coalesce(a.schema_class, ''), 'UTF8'), 'hex'),encode(convert_to(coalesce(a.target_schema_version, ''), 'UTF8'), 'hex'),encode(convert_to(a.canonical_uri, 'UTF8'), 'hex'),encode(convert_to(a.payload_media_type, 'UTF8'), 'hex'),encode(convert_to(a.payload_digest, 'UTF8'), 'hex'),encode(convert_to(a.payload_size::text, 'UTF8'), 'hex'),encode(convert_to(coalesce(a.extensions::text, ''), 'UTF8'), 'hex')], ':') FROM schema_registry.release_artifacts AS ra JOIN schema_registry.artifacts AS a ON a.artifact_revision = ra.artifact_revision WHERE ra.schema_version = $1 ORDER BY ra.kind COLLATE "C", ra.artifact_type COLLATE "C"`, nil
	case model.ProjectionResource:
		return `SELECT array_to_string(ARRAY[encode(convert_to(ra.artifact_revision, 'UTF8'), 'hex'),encode(convert_to(resource.name, 'UTF8'), 'hex'),encode(convert_to(resource.role, 'UTF8'), 'hex'),encode(convert_to(resource.media_type, 'UTF8'), 'hex'),encode(convert_to(resource.digest, 'UTF8'), 'hex'),encode(convert_to(resource.size::text, 'UTF8'), 'hex')], ':') FROM schema_registry.release_artifacts AS ra JOIN schema_registry.artifact_resources AS resource ON resource.artifact_revision = ra.artifact_revision WHERE ra.schema_version = $1 ORDER BY ra.artifact_revision COLLATE "C", resource.name COLLATE "C"`, nil
	case model.ProjectionDependency:
		return `SELECT array_to_string(ARRAY[encode(convert_to(dependency.artifact_revision, 'UTF8'), 'hex'),encode(convert_to(dependency.dependency_kind, 'UTF8'), 'hex'),encode(convert_to(dependency.dependency_artifact_type, 'UTF8'), 'hex'),encode(convert_to(dependency.dependency_revision, 'UTF8'), 'hex')], ':') FROM schema_registry.release_artifacts AS ra JOIN schema_registry.artifact_dependencies AS dependency ON dependency.artifact_revision = ra.artifact_revision WHERE ra.schema_version = $1 ORDER BY dependency.artifact_revision COLLATE "C", dependency.dependency_kind COLLATE "C", dependency.dependency_artifact_type COLLATE "C", dependency.dependency_revision COLLATE "C"`, nil
	case model.ProjectionBinding:
		return `SELECT array_to_string(ARRAY[encode(convert_to(schema_revision, 'UTF8'), 'hex'),encode(convert_to(context_revision, 'UTF8'), 'hex')], ':') FROM schema_registry.schema_context_bindings WHERE schema_version = $1 ORDER BY schema_revision COLLATE "C", context_revision COLLATE "C"`, nil
	case model.ProjectionURI:
		return `SELECT array_to_string(ARRAY[encode(convert_to(uri, 'UTF8'), 'hex'),encode(convert_to(artifact_revision, 'UTF8'), 'hex'),encode(convert_to(coalesce(resource_name, ''), 'UTF8'), 'hex')], ':') FROM schema_registry.uri_catalog_entries WHERE schema_version = $1 ORDER BY uri COLLATE "C"`, nil
	case model.ProjectionStorage:
		return `SELECT array_to_string(ARRAY[encode(convert_to(artifact_type, 'UTF8'), 'hex'),encode(convert_to(store, 'UTF8'), 'hex')], ':') FROM schema_registry.storage_catalog_entries WHERE schema_version = $1 ORDER BY artifact_type COLLATE "C", store COLLATE "C"`, nil
	case model.ProjectionSourceMapping:
		return `SELECT array_to_string(ARRAY[encode(convert_to(mapping_revision, 'UTF8'), 'hex'),encode(convert_to(source_artifact_type, 'UTF8'), 'hex'),encode(convert_to(source_revision, 'UTF8'), 'hex'),encode(convert_to(target_artifact_type, 'UTF8'), 'hex'),encode(convert_to(target_revision, 'UTF8'), 'hex'),encode(convert_to(target_schema_version, 'UTF8'), 'hex'),encode(convert_to(cardinality, 'UTF8'), 'hex'),encode(convert_to(engine_language, 'UTF8'), 'hex'),encode(convert_to(engine_language_version, 'UTF8'), 'hex'),encode(convert_to(execution_profile, 'UTF8'), 'hex'),encode(convert_to(program_name, 'UTF8'), 'hex'),encode(convert_to(program_digest, 'UTF8'), 'hex')], ':') FROM schema_registry.source_mapping_details WHERE schema_version = $1 ORDER BY source_artifact_type COLLATE "C"`, nil
	default:
		return "", fmt.Errorf("unknown projection family %q", family)
	}
}

// ProjectionCommitment hashes length-prefixed projection rows. The importer
// passes sorted rows derived from one canonical manifest; requests compare it.
func ProjectionCommitment(rows []string) []byte {
	copyRows := append([]string(nil), rows...)
	sort.Strings(copyRows)
	hash := sha256.New()
	for _, row := range copyRows {
		hash.Write([]byte(strconv.Itoa(len(row))))
		hash.Write([]byte{':'})
		hash.Write([]byte(row))
	}
	return hash.Sum(nil)
}

// ProjectionCommitmentHex returns the commitment in a test-friendly encoding.
func ProjectionCommitmentHex(rows []string) string {
	return hex.EncodeToString(ProjectionCommitment(rows))
}

func scanRelease(row pgx.Row) (model.Release, error) {
	var release model.Release
	err := row.Scan(
		&release.SchemaVersion, &release.ManifestDigest, &release.ManifestBytes, &release.PreviousSchemaVersion,
		&release.PreviousManifestDigest, &release.SourceRepository, &release.SourceCommit, &release.Changes,
		&release.ProjectionCommitment, &release.ActivationSequence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Release{}, ErrNotFound
	}
	if err != nil {
		return model.Release{}, fmt.Errorf("scan release: %w", err)
	}
	return release, nil
}

type releaseRow struct {
	release  model.Release
	sequence int64
}

func scanReleaseRow(row pgx.Row) (releaseRow, error) {
	var result releaseRow
	err := row.Scan(
		&result.release.SchemaVersion, &result.release.ManifestDigest, &result.release.ManifestBytes,
		&result.release.PreviousSchemaVersion, &result.release.PreviousManifestDigest,
		&result.release.SourceRepository, &result.release.SourceCommit, &result.release.Changes,
		&result.sequence, &result.release.ProjectionCommitment,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return releaseRow{}, ErrNotFound
	}
	if err != nil {
		return releaseRow{}, fmt.Errorf("scan release row: %w", err)
	}
	result.release.ActivationSequence = result.sequence
	return result, nil
}

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
		if !validBytes(artifact.Payload.Bytes, artifact.Payload.Size, artifact.Payload.Digest) {
			return nil, ErrInconsistentState
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return artifacts, nil
}

func validateReleaseChain(releases []releaseRow) error {
	for index, release := range releases {
		if release.sequence <= 0 {
			return ErrInconsistentState
		}
		if index == len(releases)-1 {
			continue
		}
		predecessor := releases[index+1]
		if predecessor.sequence != release.sequence-1 ||
			release.release.PreviousSchemaVersion == nil || release.release.PreviousManifestDigest == nil ||
			*release.release.PreviousSchemaVersion != predecessor.release.SchemaVersion || *release.release.PreviousManifestDigest != predecessor.release.ManifestDigest {
			return ErrInconsistentState
		}
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// StaticQuery contains an auditable code-owned SQL query. Request values are
// always pgx parameters and never become SQL syntax.
type StaticQuery string

// ValidSort verifies that callers select only documented code-owned orderings.
func ValidSort(value string) bool {
	switch value {
	case "activation-desc", "kind-type", "artifact-type", "revision", "store":
		return true
	default:
		return false
	}
}

// FilterValues rejects values that cannot fit a bounded indexed predicate.
func FilterValues(values ...string) error {
	for _, value := range values {
		if value == "" || len(value) > 8192 || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("invalid bounded filter value")
		}
	}
	return nil
}
