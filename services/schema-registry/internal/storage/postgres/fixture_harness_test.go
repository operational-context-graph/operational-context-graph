// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
)

const fixtureOrigin = "https://registry.example.test"

// registryFixture is a complete, format-validated candidate release. All data
// reaches PostgreSQL only after the shared format validator accepts it.
type registryFixture struct {
	name      string
	version   string
	previous  *manifest.Release
	artifacts []artifact.Artifact
}

func TestFixtureCatalogPassesSharedValidation(t *testing.T) {
	fixtures := fixtureCatalog(t)
	require.Len(t, fixtures, 9)
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			parsed, validationContext := fixtureManifest(t, fixture)
			require.NoError(t, parsed.Validate(validationContext))
		})
	}
}

func TestFixtureLoadSuite(t *testing.T) {
	runPostgreSQLSuite(t, new(fixtureLoadSuite))
}

type fixtureLoadSuite struct {
	PostgreSQLSuite
}

func (suite *fixtureLoadSuite) TestLoadValidatedFixture() {
	for _, fixture := range fixtureCatalog(suite.T()) {
		parsed, loadErr := loadValidatedFixture(suite.T(), suite.Context, suite.Database, fixture)
		suite.Require().NoError(loadErr, fixture.name)
		var count int
		suite.Require().NoError(suite.Database.QueryRowContext(suite.Context, `SELECT count(*) FROM schema_registry.release_artifacts WHERE schema_version = $1`, parsed.SchemaVersion).Scan(&count), fixture.name)
		suite.Require().Len(fixture.artifacts, count, fixture.name)
		verifyLoadedFixture(suite.T(), suite.Context, suite.Database, fixture)
	}
}

func verifyLoadedFixture(t *testing.T, ctx context.Context, database *sql.DB, fixture registryFixture) {
	t.Helper()
	switch fixture.name {
	case "boolean-schema":
		var payload []byte
		require.NoError(t, database.QueryRowContext(ctx, `SELECT payload_bytes FROM schema_registry.artifacts WHERE artifact_revision = 'rev-boolean-v1'`).Scan(&payload))
		require.Equal(t, []byte("false"), payload)
	case "empty-byte-resource":
		var size int64
		var length int
		require.NoError(t, database.QueryRowContext(ctx, `SELECT size, octet_length(bytes) FROM schema_registry.artifact_resources WHERE artifact_revision = 'rev-empty-resource-v1' AND name = 'empty'`).Scan(&size, &length))
		require.Zero(t, size)
		require.Zero(t, length)
	case "exact-size":
		var size int64
		require.NoError(t, database.QueryRowContext(ctx, `SELECT payload_size FROM schema_registry.artifacts WHERE artifact_revision = 'rev-exact-size-v1'`).Scan(&size))
		require.EqualValues(t, artifact.MaxArtifactBytes, size)
	case "1001-items":
		var count int
		require.NoError(t, database.QueryRowContext(ctx, `SELECT count(*) FROM schema_registry.release_artifacts WHERE schema_version = '6.0.0'`).Scan(&count))
		require.Equal(t, 1001, count)
	case "maximum-serviceable":
		var size int64
		require.NoError(t, database.QueryRowContext(ctx, `SELECT size FROM schema_registry.artifact_resources WHERE artifact_revision = 'rev-maximum-resource-v1' AND name = 'maximum'`).Scan(&size))
		require.EqualValues(t, artifact.MaxArtifactBytes, size)
	}
}

func fixtureCatalog(t *testing.T) []registryFixture {
	t.Helper()
	initial := registryFixture{
		name: "initial", version: "1.0.0",
		artifacts: []artifact.Artifact{fixtureSchema("device", "rev-device-v1", "1.0.0", []byte("true"), nil)},
	}
	initialRelease := validatedFixture(t, initial)
	multi := registryFixture{
		name: "multi-release", version: "1.1.0", previous: &initialRelease,
		artifacts: []artifact.Artifact{
			fixtureSchema("device", "rev-device-v1", "1.0.0", []byte("true"), nil),
			fixtureSchema("location", "rev-location-v1", "1.1.0", []byte(`{"type":"object"}`), nil),
		},
	}
	multiRelease := validatedFixture(t, multi)
	rollback := registryFixture{
		name: "rollback", version: "2.0.0", previous: &multiRelease,
		artifacts: []artifact.Artifact{fixtureSchema("device", "rev-device-v1", "1.0.0", []byte("true"), nil)},
	}
	inactive := registryFixture{
		name: "inactive", version: "2.1.0", previous: &multiRelease,
		artifacts: []artifact.Artifact{fixtureSchema("inactive", "rev-inactive-v1", "2.1.0", []byte("true"), nil)},
	}
	booleanSchema := registryFixture{
		name: "boolean-schema", version: "3.0.0",
		artifacts: []artifact.Artifact{fixtureSchema("boolean", "rev-boolean-v1", "3.0.0", []byte("false"), nil)},
	}
	emptyByte := registryFixture{
		name: "empty-byte-resource", version: "4.0.0",
		artifacts: []artifact.Artifact{fixtureSchema("empty-resource", "rev-empty-resource-v1", "4.0.0", []byte("true"), []artifact.Resource{fixtureResource("empty", "auxiliary", "application/octet-stream", nil)})},
	}
	exactSize := registryFixture{
		name: "exact-size", version: "5.0.0",
		artifacts: []artifact.Artifact{fixtureSchema("exact-size", "rev-exact-size-v1", "5.0.0", append([]byte("true"), []byte(strings.Repeat(" ", artifact.MaxArtifactBytes-4))...), nil)},
	}
	many := registryFixture{name: "1001-items", version: "6.0.0", artifacts: make([]artifact.Artifact, 0, 1001)}
	for number := 0; number < 1001; number++ {
		artifactType := fmt.Sprintf("item-%04d", number)
		many.artifacts = append(many.artifacts, fixtureSchema(artifactType, "rev-"+artifactType, "6.0.0", []byte("true"), nil))
	}
	maximumServiceable := registryFixture{
		name: "maximum-serviceable", version: "7.0.0",
		artifacts: []artifact.Artifact{fixtureSchema("maximum-resource", "rev-maximum-resource-v1", "7.0.0", []byte("true"), []artifact.Resource{fixtureResource("maximum", "auxiliary", "application/octet-stream", make([]byte, artifact.MaxArtifactBytes))})},
	}
	return []registryFixture{initial, multi, rollback, inactive, booleanSchema, emptyByte, exactSize, many, maximumServiceable}
}

func fixtureSchema(artifactType, revision, created string, payload []byte, resources []artifact.Resource) artifact.Artifact {
	return artifact.Artifact{
		Kind: artifact.KindSchema, Type: artifactType, Revision: revision, CreatedUnder: created,
		SchemaClass:  artifact.SchemaClassEntity,
		CanonicalURI: fixtureOrigin + "/v1/resources/schema/" + artifactType + "/" + revision,
		Payload:      fixtureDescriptor(payload, artifact.SchemaMediaType, "schemas/"+artifactType+".json"),
		PayloadBytes: payload,
		Resources:    resources,
	}
}

func fixtureResource(name, role, mediaType string, bytes []byte) artifact.Resource {
	return artifact.Resource{Name: name, Role: role, Descriptor: fixtureDescriptor(bytes, mediaType, "resources/"+name), Bytes: bytes}
}

func fixtureDescriptor(bytes []byte, mediaType, bundlePath string) artifact.Descriptor {
	sum := sha256.Sum256(bytes)
	return artifact.Descriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(sum[:]), Size: int64(len(bytes)), BundlePath: bundlePath}
}

func validatedFixture(t *testing.T, fixture registryFixture) manifest.Release {
	t.Helper()
	parsed, context := fixtureManifest(t, fixture)
	require.NoError(t, parsed.Validate(context), fixture.name)
	return manifest.Release{Manifest: parsed, Digest: fixtureDigest(parsed.Raw)}
}

func fixtureManifest(t testing.TB, fixture registryFixture) (manifest.Manifest, manifest.Context) {
	t.Helper()
	entries := make([]map[string]any, 0, len(fixture.artifacts))
	uris := make([]map[string]any, 0, len(fixture.artifacts))
	storage := make([]map[string]any, 0, len(fixture.artifacts))
	bound := make(map[manifest.Key]artifact.Artifact, len(fixture.artifacts))
	for _, item := range fixture.artifacts {
		resourceEntries := make([]map[string]any, 0, len(item.Resources))
		for _, resource := range item.Resources {
			resourceEntries = append(resourceEntries, descriptorMap(resource.Descriptor, map[string]any{"name": resource.Name, "role": resource.Role}))
		}
		entries = append(entries, map[string]any{
			"kind": item.Kind, "artifactType": item.Type, "artifactRevision": item.Revision,
			"createdUnderSchemaVersion": item.CreatedUnder, "schemaClass": item.SchemaClass,
			"canonicalUri": item.CanonicalURI, "payload": descriptorMap(item.Payload, nil),
			"resources": resourceEntries, "dependencies": []any{},
		})
		uris = append(uris, map[string]any{"uri": item.CanonicalURI, "artifactRevision": item.Revision})
		storage = append(storage, map[string]any{"kind": item.Kind, "artifactType": item.Type, "stores": []string{"data-center-inventory"}})
		bound[manifest.Key{Kind: item.Kind, Type: item.Type, Revision: item.Revision}] = item
	}
	root := map[string]any{
		"$schema": "https://example.test/manifest-1.0.0", "formatVersion": "1.0.0", "schemaVersion": fixture.version,
		"source":    map[string]any{"repository": "https://git.example.test/schema-model", "commit": strings.Repeat("a", 40)},
		"artifacts": entries, "schemaContextBindings": []any{}, "uriCatalog": uris, "storageCatalog": storage,
	}
	history := make([]manifest.Release, 0)
	if fixture.previous == nil {
		root["previousRelease"] = nil
	} else {
		root["previousRelease"] = map[string]any{"schemaVersion": fixture.previous.Manifest.SchemaVersion, "manifestDigest": fixture.previous.Digest}
		history = append(history, *fixture.previous)
	}
	encoded, err := json.Marshal(root)
	require.NoError(t, err)
	canonical, err := jcs.Transform(encoded)
	require.NoError(t, err)
	parsed, err := manifest.Parse(canonical)
	require.NoError(t, err)
	return parsed, manifest.Context{RegistryOrigin: fixtureOrigin, Artifacts: bound, History: history}
}

func descriptorMap(descriptor artifact.Descriptor, additions map[string]any) map[string]any {
	result := map[string]any{
		"mediaType": descriptor.MediaType, "digest": descriptor.Digest, "size": descriptor.Size, "bundlePath": descriptor.BundlePath,
	}
	for key, value := range additions {
		result[key] = value
	}
	return result
}

func fixtureDigest(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return "sha-256:" + hex.EncodeToString(sum[:])
}

func loadValidatedFixture(t testing.TB, ctx context.Context, database *sql.DB, fixture registryFixture) (manifest.Manifest, error) {
	t.Helper()
	parsed, validationContext := fixtureManifest(t, fixture)
	if err := parsed.Validate(validationContext); err != nil {
		return manifest.Manifest{}, fmt.Errorf("validate fixture %s: %w", fixture.name, err)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return manifest.Manifest{}, err
	}
	defer func() { _ = tx.Rollback() }()
	commitmentRows, err := manifestProjectionRows(parsed, parsed.Raw, model.ProjectionRelease)
	if err != nil {
		return manifest.Manifest{}, err
	}
	commitment := ProjectionCommitment(commitmentRows)
	_, err = tx.ExecContext(ctx, `
INSERT INTO schema_registry.releases (schema_version, manifest_digest, manifest_bytes, previous_schema_version, previous_manifest_digest, source_repository, source_commit, projection_commitment)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		parsed.SchemaVersion, fixtureDigest(parsed.Raw), parsed.Raw, previousVersion(parsed), previousDigest(parsed), parsed.Source.Repository, parsed.Source.Commit, commitment)
	if err != nil {
		return manifest.Manifest{}, err
	}
	for _, item := range fixture.artifacts {
		_, err = tx.ExecContext(ctx, `
INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (artifact_revision) DO NOTHING`, item.Revision, item.Kind, item.Type, item.CreatedUnder, item.SchemaClass, item.CanonicalURI, item.Payload.MediaType, item.Payload.Digest, item.Payload.Size, item.PayloadBytes)
		if err != nil {
			return manifest.Manifest{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_registry.release_artifacts (schema_version, kind, artifact_type, artifact_revision) VALUES ($1, $2, $3, $4)`, parsed.SchemaVersion, item.Kind, item.Type, item.Revision)
		if err != nil {
			return manifest.Manifest{}, err
		}
		for _, resource := range item.Resources {
			_, err = tx.ExecContext(ctx, `
INSERT INTO schema_registry.artifact_resources (artifact_revision, name, role, media_type, digest, size, bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (artifact_revision, name) DO NOTHING`, item.Revision, resource.Name, resource.Role, resource.MediaType, resource.Digest, resource.Size, databaseBytes(resource.Bytes))
			if err != nil {
				return manifest.Manifest{}, err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_registry.uri_catalog_entries (schema_version, uri, artifact_revision) VALUES ($1, $2, $3)`, parsed.SchemaVersion, item.CanonicalURI, item.Revision)
		if err != nil {
			return manifest.Manifest{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_registry.storage_catalog_entries (schema_version, artifact_type, store) VALUES ($1, $2, 'data-center-inventory')`, parsed.SchemaVersion, item.Type)
		if err != nil {
			return manifest.Manifest{}, err
		}
	}
	if err := storeProjectionCommitments(ctx, tx, parsed); err != nil {
		return manifest.Manifest{}, err
	}
	if err := tx.Commit(); err != nil {
		return manifest.Manifest{}, err
	}
	return parsed, nil
}

// prepopulateReadModelState loads externally supplied visibility inputs for a
// read-only test fixture. It does not model or exercise a release transition.
func prepopulateReadModelState(ctx context.Context, database *sql.DB, current string, sequences map[string]int64) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for version, sequence := range sequences {
		if _, err := tx.ExecContext(ctx, `UPDATE schema_registry.releases SET activated_sequence = $2 WHERE schema_version = $1`, version, sequence); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_registry.activated_revision_reachability (artifact_revision, first_activated_sequence)
SELECT selection.artifact_revision, release.activated_sequence
FROM schema_registry.release_artifacts AS selection
JOIN schema_registry.releases AS release ON release.schema_version = selection.schema_version
WHERE release.activated_sequence IS NOT NULL
ON CONFLICT (artifact_revision) DO NOTHING`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_registry.registry_state SET current_schema_version = $1 WHERE singleton = true`, current); err != nil {
		return err
	}
	return tx.Commit()
}

func storeProjectionCommitments(ctx context.Context, transaction *sql.Tx, value manifest.Manifest) error {
	for _, family := range []model.ProjectionFamily{model.ProjectionRelease, model.ProjectionArtifact, model.ProjectionResource, model.ProjectionDependency, model.ProjectionBinding, model.ProjectionURI, model.ProjectionStorage, model.ProjectionSourceMapping} {
		rows, err := manifestProjectionRows(value, value.Raw, family)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_registry.release_projection_commitments (schema_version, family, commitment) VALUES ($1, $2, $3)`, value.SchemaVersion, family, ProjectionCommitment(rows)); err != nil {
			return err
		}
	}
	return nil
}

func previousVersion(value manifest.Manifest) *string {
	if value.PreviousRelease == nil {
		return nil
	}
	return &value.PreviousRelease.SchemaVersion
}

func databaseBytes(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

func previousDigest(value manifest.Manifest) *string {
	if value.PreviousRelease == nil {
		return nil
	}
	return &value.PreviousRelease.ManifestDigest
}
