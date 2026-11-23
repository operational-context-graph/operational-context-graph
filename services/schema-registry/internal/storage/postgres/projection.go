// Authoritative registry integrity verification. The functionality in this file checks whether
// the database faithfully represent authoritative release manifests and descriptors.

package postgres

import (
	"bytes"
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

	"github.com/jackc/pgx/v5"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

// VerifyProjection directly compares an affected projection family with authoritative manifest and digest-verified descriptor data.
// Missing, extra, and altered rows invalidate the complete response.
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
	return nil
}

// verifyRevisionProjection checks the requested family in every activated release selecting a revision.
// It returns ErrNotFound when no activated selection establishes visibility.
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
	versions := make([]string, 0)
	for rows.Next() {
		var schemaVersion string
		if err := rows.Scan(&schemaVersion); err != nil {
			return fmt.Errorf("scan artifact release selection: %w", err)
		}
		versions = append(versions, schemaVersion)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate artifact release selections: %w", err)
	}
	// pgx permits only one active query on this transaction's connection.
	rows.Close()
	if len(versions) == 0 {
		return ErrNotFound
	}
	for _, schemaVersion := range versions {
		if err := s.VerifyProjection(ctx, schemaVersion, family); err != nil {
			return err
		}
	}
	return nil
}

// sourceMappingProjectionRows derives sorted mapping rows from digest-verified descriptor payloads selected by a manifest.
func sourceMappingProjectionRows(ctx context.Context, tx pgx.Tx, value manifest.Manifest) ([]string, error) {
	rows := make([]string, 0)
	for _, item := range value.Artifacts {
		if item.Kind != "source-mapping" {
			continue
		}
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT payload_bytes FROM schema_registry.artifacts WHERE artifact_revision = $1`, item.Revision).Scan(&payload); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("read source-mapping descriptor: %w", ErrInconsistentState)
			}
			return nil, fmt.Errorf("read source-mapping descriptor: %w", err)
		}
		if manifestDigest(payload) != item.Payload.Digest || int64(len(payload)) != item.Payload.Size {
			return nil, ErrInconsistentState
		}
		descriptor, err := sourcemapping.Parse(payload)
		if err != nil {
			return nil, fmt.Errorf("parse source-mapping descriptor: %w", ErrInconsistentState)
		}
		derived, err := mappingDescriptorProjection(value, item, descriptor)
		if err != nil {
			return nil, err
		}
		rows = append(rows, derived...)
	}
	sort.Strings(rows)
	return rows, nil
}

// mappingDescriptorProjection resolves descriptor references against selected dependencies and program resources.
// It rejects unused programs and encodes transformation ordinals so comparisons preserve execution order.
func mappingDescriptorProjection(value manifest.Manifest, item artifact.Artifact, descriptor sourcemapping.Descriptor) ([]string, error) {
	if descriptor.ID != item.Type {
		return nil, ErrInconsistentState
	}
	resolve := func(reference sourcemapping.SchemaReference, canonical bool) (string, error) {
		for _, dependency := range item.Dependencies {
			if dependency.Kind != "schema" || dependency.Type != reference.ID {
				continue
			}
			if reference.Revision != "" && reference.Revision != dependency.Revision || canonical && reference.Version != "" && reference.Version != item.TargetSchema {
				return "", ErrInconsistentState
			}
			for _, selected := range value.Artifacts {
				if selected.Kind == dependency.Kind && selected.Type == dependency.Type && selected.Revision == dependency.Revision {
					if canonical && selected.SchemaClass != "entity-type" && selected.SchemaClass != "identifier-type" && selected.SchemaClass != "edge-type" {
						return "", ErrInconsistentState
					}
					return dependency.Revision, nil
				}
			}
		}
		return "", ErrInconsistentState
	}
	referenced := make(map[string]bool)
	bind := func(reference sourcemapping.ProgramReference) error {
		for _, resource := range item.Resources {
			if resource.Name == reference.ProgramRef && resource.Role == "program" && resource.Digest == reference.ProgramDigest {
				referenced[resource.Name] = true
				return nil
			}
		}
		return ErrInconsistentState
	}
	source, err := resolve(descriptor.SourceSchema, false)
	if err != nil {
		return nil, err
	}
	conditionName, conditionDigest := "", ""
	if condition := descriptor.Programs.Condition; condition != nil {
		if err := bind(*condition); err != nil {
			return nil, err
		}
		conditionName, conditionDigest = condition.ProgramRef, condition.ProgramDigest
	}
	rows := []string{projectionRow("mapping", item.Revision, descriptor.SourceSchema.ID, source, item.TargetSchema, descriptor.Engine.Language, descriptor.Engine.LanguageVersion, descriptor.Engine.ExecutionProfile, conditionName, conditionDigest)}
	for ordinal, transformation := range descriptor.Programs.Transformations {
		target, err := resolve(transformation.TargetSchema, true)
		if err != nil {
			return nil, err
		}
		if err := bind(transformation.ProgramReference); err != nil {
			return nil, err
		}
		rows = append(rows, projectionRow("transformation", item.Revision, strconv.Itoa(ordinal), transformation.TargetSchema.ID, target, string(transformation.Cardinality), transformation.ProgramRef, transformation.ProgramDigest, "", ""))
	}
	for _, resource := range item.Resources {
		if resource.Role == "program" && !referenced[resource.Name] {
			return nil, ErrInconsistentState
		}
	}
	return rows, nil
}

// releaseManifestBytes loads authoritative bytes only for an activated release, returning ErrNotFound otherwise.
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

// manifestProjectionRows derives sorted comparison rows for fields embedded in an authoritative manifest.
// Nonempty source-mapping families require descriptor bytes and return ErrInconsistentState here.
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
				rows = append(rows, projectionRow(item.Revision, resource.Name, string(resource.Role), resource.MediaType, resource.Digest, strconv.FormatInt(resource.Size, 10)))
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
				rows = append(rows, projectionRow(entry.ArtifactType, string(store)))
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

// manifestDigest returns the registry's lowercase, prefixed SHA-256 digest of exact bytes.
func manifestDigest(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return "sha-256:" + hex.EncodeToString(sum[:])
}

// projectionRow hex-encodes fields before joining them, preserving boundaries even when fields contain delimiters.
func projectionRow(fields ...string) string {
	encoded := make([]string, len(fields))
	for index, field := range fields {
		encoded[index] = hex.EncodeToString([]byte(field))
	}
	return strings.Join(encoded, ":")
}

// canonicalJSON normalizes extension values for projection comparison; nil maps and marshal failures produce an empty string.
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

// validBytes checks declared size, the 16 MiB artifact limit, and the digest of exact stored bytes.
func validBytes(bytes []byte, size int64, digest string) bool {
	if int64(len(bytes)) != size || size < 0 || size > 16*1024*1024 {
		return false
	}
	return manifestDigest(bytes) == digest
}

// requireActiveRelease returns ErrNotFound unless the selected release has an activation sequence.
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

// projectionRows loads comparable indexed rows and normalizes extension JSON without losing numeric precision.
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
		var fields []string
		var err error
		if family == model.ProjectionSourceMapping {
			err = rows.Scan(&fields)
			value = projectionRow(fields...)
		} else {
			err = rows.Scan(&value)
		}
		if err != nil {
			return nil, fmt.Errorf("scan %s projection: %w", family, err)
		}
		if family == model.ProjectionArtifact {
			fields := strings.Split(value, ":")
			if len(fields) != 11 {
				return nil, ErrInconsistentState
			}
			if fields[10] != "" {
				raw, err := hex.DecodeString(fields[10])
				if err != nil {
					return nil, ErrInconsistentState
				}
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				// Float conversion could change extension numbers and falsely disagree with authoritative manifest values.
				var extensions any
				if decodeErr := decoder.Decode(&extensions); decodeErr != nil {
					return nil, ErrInconsistentState
				}
				canonical, err := json.Marshal(extensions)
				if err != nil {
					return nil, ErrInconsistentState
				}
				fields[10] = hex.EncodeToString(canonical)
				value = strings.Join(fields, ":")
			}
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s projection: %w", family, err)
	}
	if family == model.ProjectionSourceMapping {
		sort.Strings(values)
	}
	return values, nil
}

// projectionQuery selects a static, fully qualified query for each supported projection family.
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
		return `SELECT ARRAY['mapping', mapping_revision, source_artifact_type, source_revision, target_schema_version, engine_language, engine_language_version, execution_profile, coalesce(condition_program_name, ''), coalesce(condition_program_digest, '')] FROM schema_registry.source_mapping_details WHERE schema_version = $1
UNION ALL SELECT ARRAY['transformation', mapping_revision, ordinal::text, target_artifact_type, target_revision, cardinality, program_name, program_digest, '', ''] FROM schema_registry.source_mapping_transformations WHERE schema_version = $1`, nil
	default:
		return "", fmt.Errorf("unknown projection family %q", family)
	}
}
