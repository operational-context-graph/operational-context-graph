// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

/*
Context and identifier relationship scope

This test uses the shared Testcontainers-backed readStoreSuite and freshly migrated PostgreSQL tables.
Shared format validators accept the complete schema-and-context fixture before insertion.
Exact bindings and Uniform Resource Identifier (URI) mappings resolve within one read snapshot.
Unbound revisions return empty bindings; missing URI entries and unknown releases return not-found errors.

fixture_harness_test.go accepts optional schema-context bindings and inserts them before storing projection commitments.
This ordering makes subsequent reads verify the complete relationship projection.
The fixture supplies read-model state without implementing or exercising a release transition.
*/

// TestContextAndURIReadRelationships loads a format-validated schema and context into freshly migrated PostgreSQL tables.
// It verifies exact context bindings and Uniform Resource Identifier (URI) mappings within one read snapshot.
// Unbound revisions return empty bindings; missing URI entries and unknown releases return not-found errors.
func (suite *readStoreSuite) TestContextAndURIReadRelationships() {
	t := suite.T()
	const version = "1.0.0"
	schema := fixtureSchema("device", "device-r1", version, []byte("true"), nil)
	contextBytes := []byte(`{"@context":{"name":"https://schema.org/name"}}`)
	contextArtifact := artifact.Artifact{
		Kind: artifact.KindContext, Type: "device", Revision: "context-r1", CreatedUnderSchemaVersion: version,
		CanonicalURI: fixtureOrigin + "/v1/resources/context/device/context-r1",
		Payload:      fixtureDescriptor(contextBytes, artifact.ContextMediaType, "contexts/device.jsonld"), PayloadBytes: contextBytes,
	}
	fixture := registryFixture{
		version: version, artifacts: []artifact.Artifact{contextArtifact, schema},
		bindings: []manifest.Binding{{SchemaRevision: schema.Revision, ContextRevisions: []string{contextArtifact.Revision}}},
	}
	parsed, validation := fixtureManifest(t, fixture)
	_, validationErr := manifest.Validate(parsed.Raw, validation)
	require.NoError(t, validationErr)
	_, err := loadValidatedFixture(t, suite.Context, suite.Database, fixture)
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(suite.Context, suite.Database, version, map[string]int64{version: 1}))
	pool, err := pgxpool.New(suite.Context, suite.DatabaseURL)
	require.NoError(t, err)
	defer pool.Close()
	store, err := storage.NewStore(pool, storage.Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	require.NoError(t, store.Read(suite.Context, func(ctx context.Context, snapshot *storage.Snapshot) error {
		bindings, err := snapshot.ContextBindings(ctx, version, schema.Revision)
		require.NoError(t, err)
		require.Equal(t, []model.SchemaContextBinding{{SchemaVersion: version, SchemaRevision: schema.Revision, ContextRevision: contextArtifact.Revision}}, bindings)
		empty, err := snapshot.ContextBindings(ctx, version, "unbound-revision")
		require.NoError(t, err)
		require.Empty(t, empty)
		for _, item := range fixture.artifacts {
			entry, uriErr := snapshot.URIEntry(ctx, version, item.CanonicalURI)
			require.NoError(t, uriErr)
			require.Equal(t, model.URIEntry{SchemaVersion: version, URI: item.CanonicalURI, ArtifactRevision: item.Revision}, entry)
		}
		_, err = snapshot.URIEntry(ctx, version, "https://missing.example.test/schema")
		require.ErrorIs(t, err, storage.ErrNotFound)
		_, err = snapshot.ContextBindings(ctx, "2.0.0", schema.Revision)
		require.ErrorIs(t, err, storage.ErrNotFound)
		return nil
	}))
	require.Zero(t, pool.Stat().AcquiredConns())
}
