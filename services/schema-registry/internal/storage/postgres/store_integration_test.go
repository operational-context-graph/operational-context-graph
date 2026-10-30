// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/stretchr/testify/require"
)

func TestReadStoreSuite(t *testing.T) {
	runPostgreSQLSuite(t, new(readStoreSuite))
}

type readStoreSuite struct {
	PostgreSQLSuite
}

func (suite *readStoreSuite) candidateStore() *Store {
	t := suite.T()
	_, err := loadValidatedFixture(t, suite.Context, suite.Database, candidateFixture(t))
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(suite.Context, suite.Database, "8.0.0", map[string]int64{"8.0.0": 1}))
	pool, err := pgxpool.New(suite.Context, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: 5 * time.Second, Query: 5 * time.Second, Statement: 5 * time.Second, Lock: 5 * time.Second, IdleInTransaction: 5 * time.Second})
	require.NoError(t, err)
	return store
}

func (suite *readStoreSuite) TestCandidateMappingsAndParameterizedFilters() {
	store := suite.candidateStore()
	require.NoError(suite.T(), store.Read(suite.Context, func(ctx context.Context, snapshot *Snapshot) error {
		items, err := snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", SourceArtifactType: "candidate-source"}, nil, nil, 1000)
		if err != nil {
			return err
		}
		suite.Require().Len(items, 2)
		filtered, err := snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", TargetArtifactType: "candidate-edge"}, nil, nil, 1000)
		if err != nil {
			return err
		}
		suite.Require().Len(filtered, 1)
		filtered, err = snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", TargetArtifactType: "candidate-entity"}, nil, nil, 1000)
		if err != nil {
			return err
		}
		suite.Require().Len(filtered, 2, "repeated target references must not duplicate mappings")
		filtered, err = snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", SourceArtifactType: "candidate-source", ArtifactType: "candidate-mapping-1", EngineLanguage: "publisher-engine", ExecutionProfile: "publisher-profile"}, nil, nil, 1)
		if err != nil {
			return err
		}
		suite.Require().Len(filtered, 1)
		suite.Equal("candidate-mapping-1", filtered[0].Type)
		detail, err := snapshot.SourceMappingByRevision(ctx, "8.0.0", "candidate-mapping-0-r1")
		if err != nil {
			return err
		}
		suite.Require().NotNil(detail.Condition)
		suite.Require().Len(detail.Transformations, 3)
		suite.Equal("many", detail.Transformations[1].Cardinality)
		suite.Equal("candidate-edge", detail.Transformations[1].TargetArtifactType)
		detail, err = snapshot.SourceMapping(ctx, "8.0.0", "candidate-mapping-1")
		if err != nil {
			return err
		}
		suite.Nil(detail.Condition)
		for _, hostile := range []string{"missing", "x' OR true --", "%", "/* query */", "SELECT", "\n", "é", strings.Repeat("x", 256)} {
			values, err := snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", SourceArtifactType: hostile, EngineLanguage: hostile}, nil, nil, 1)
			if err != nil {
				return err
			}
			suite.Empty(values)
		}
		return nil
	}))
}

func (suite *readStoreSuite) TestOrderedMappingProjectionDrift() {
	store := suite.candidateStore()
	_, err := suite.Database.ExecContext(suite.Context, `UPDATE schema_registry.source_mapping_transformations SET cardinality = 'many' WHERE mapping_revision = 'candidate-mapping-0-r1' AND ordinal = 0`)
	suite.Require().NoError(err)
	err = store.Read(suite.Context, func(ctx context.Context, snapshot *Snapshot) error {
		_, err := snapshot.SourceMappingByRevision(ctx, "8.0.0", "candidate-mapping-0-r1")
		return err
	})
	suite.ErrorIs(err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestCandidateAncillaryResources() {
	fixture := candidateFixture(suite.T())
	data := []byte(`{"code":"value"}`)
	ancillary := fixtureResource("lookup.json", "metadata", "application/json", data)
	mapping := &fixture.artifacts[3]
	mapping.Resources = append([]artifact.Resource{ancillary}, mapping.Resources...)
	_, err := loadValidatedFixture(suite.T(), suite.Context, suite.Database, fixture)
	suite.Require().NoError(err)
	suite.Require().NoError(prepopulateReadModelState(suite.Context, suite.Database, "8.0.0", map[string]int64{"8.0.0": 1}))
	pool, err := pgxpool.New(suite.Context, suite.DatabaseURL)
	suite.Require().NoError(err)
	suite.T().Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	suite.Require().NoError(err)
	suite.Require().NoError(store.Read(suite.Context, func(ctx context.Context, snapshot *Snapshot) error {
		resources, err := snapshot.Resources(ctx, mapping.Revision)
		if err != nil {
			return err
		}
		suite.Require().Len(resources, 3)
		suite.Equal("lookup.json", resources[0].Name)
		suite.Equal("metadata", resources[0].Role)
		for _, resource := range resources {
			suite.Nil(resource.Bytes, "resource lists must remain metadata-only")
		}
		detail, err := snapshot.SourceMappingByRevision(ctx, fixture.version, mapping.Revision)
		if err != nil {
			return err
		}
		suite.Require().NotNil(detail.Condition)
		suite.Equal("condition.bin", detail.Condition.ProgramName)
		suite.Require().Len(detail.Transformations, 3)
		dependencies, err := snapshot.Dependencies(ctx, mapping.Revision)
		if err != nil {
			return err
		}
		suite.Len(dependencies, 3)
		resource, err := snapshot.Resource(ctx, mapping.Revision, ancillary.Name)
		if err != nil {
			return err
		}
		suite.Equal(data, resource.Bytes)
		suite.Equal(ancillary.Digest, resource.Digest)
		return nil
	}))
}

func (suite *readStoreSuite) TestRevisionProjectionReadsVerifyAllSelections() {
	fixtures := fixtureCatalog(suite.T())
	for _, fixture := range fixtures[:3] {
		_, err := loadValidatedFixture(suite.T(), suite.Context, suite.Database, fixture)
		suite.Require().NoError(err)
	}
	versions := map[string]int64{"1.0.0": 1, "1.1.0": 2, "2.0.0": 3}
	suite.Require().NoError(prepopulateReadModelState(suite.Context, suite.Database, "2.0.0", versions))
	pool, err := pgxpool.New(suite.Context, suite.DatabaseURL)
	suite.Require().NoError(err)
	suite.T().Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	suite.Require().NoError(err)
	for _, family := range []model.ProjectionFamily{model.ProjectionResource, model.ProjectionDependency} {
		read := func(ctx context.Context, snapshot *Snapshot) error {
			if family == model.ProjectionResource {
				_, err := snapshot.Resources(ctx, "rev-device-v1")
				return err
			}
			_, err := snapshot.Dependencies(ctx, "rev-device-v1")
			return err
		}
		suite.Require().NoError(store.Read(suite.Context, read))
		for version := range versions {
			var commitment []byte
			suite.Require().NoError(suite.Database.QueryRowContext(suite.Context, `SELECT commitment FROM schema_registry.release_projection_commitments WHERE schema_version = $1 AND family = $2`, version, family).Scan(&commitment))
			_, err := suite.Database.ExecContext(suite.Context, `UPDATE schema_registry.release_projection_commitments SET commitment = $1 WHERE schema_version = $2 AND family = $3`, make([]byte, 32), version, family)
			suite.Require().NoError(err)
			suite.ErrorIs(store.Read(suite.Context, read), ErrInconsistentState, "family %s in release %s must remain verified", family, version)
			_, err = suite.Database.ExecContext(suite.Context, `UPDATE schema_registry.release_projection_commitments SET commitment = $1 WHERE schema_version = $2 AND family = $3`, commitment, version, family)
			suite.Require().NoError(err)
		}
	}
}

func (suite *readStoreSuite) requireMappingProjectionFailure(statement string) {
	store := suite.candidateStore()
	_, err := suite.Database.ExecContext(suite.Context, statement)
	suite.Require().NoError(err)
	err = store.Read(suite.Context, func(ctx context.Context, snapshot *Snapshot) error {
		_, err := snapshot.SourceMappingByRevision(ctx, "8.0.0", "candidate-mapping-0-r1")
		return err
	})
	suite.ErrorIs(err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestMissingMappingConditionProjection() {
	suite.requireMappingProjectionFailure(`UPDATE schema_registry.source_mapping_details SET condition_program_name = NULL, condition_program_digest = NULL WHERE mapping_revision = 'candidate-mapping-0-r1'`)
}

func (suite *readStoreSuite) TestExtraMappingTransformationProjection() {
	suite.requireMappingProjectionFailure(`INSERT INTO schema_registry.source_mapping_transformations SELECT schema_version,mapping_revision,3,target_artifact_type,target_revision,cardinality,program_name,program_digest FROM schema_registry.source_mapping_transformations WHERE mapping_revision = 'candidate-mapping-0-r1' AND ordinal = 0`)
}

func (suite *readStoreSuite) TestMissingMappingTransformationProjection() {
	suite.requireMappingProjectionFailure(`DELETE FROM schema_registry.source_mapping_transformations WHERE mapping_revision = 'candidate-mapping-0-r1' AND ordinal = 2`)
}

func (suite *readStoreSuite) TestReorderedMappingTransformationProjection() {
	suite.requireMappingProjectionFailure(`UPDATE schema_registry.source_mapping_transformations SET ordinal = ordinal + 10 WHERE mapping_revision = 'candidate-mapping-0-r1'`)
}

func (suite *readStoreSuite) TestMappingCommitmentMismatch() {
	suite.requireMappingProjectionFailure(`UPDATE schema_registry.release_projection_commitments SET commitment = decode(repeat('00',32),'hex') WHERE schema_version = '8.0.0' AND family = 'source-mapping'`)
}

func (suite *readStoreSuite) TestConcurrentCandidateSnapshots() {
	store := suite.candidateStore()
	var group sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- store.Read(suite.Context, func(ctx context.Context, snapshot *Snapshot) error {
				items, err := snapshot.ReleaseArtifacts(ctx, "8.0.0", model.ArtifactFilter{Kind: "source-mapping", SourceArtifactType: "candidate-source"}, nil, nil, 1)
				if err != nil {
					return err
				}
				if len(items) != 1 || snapshot.CurrentState().SchemaVersion != "8.0.0" {
					return ErrInconsistentState
				}
				return nil
			})
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		suite.NoError(err)
	}
}

func (suite *readStoreSuite) TestReleasesRejectsMissingInitialActivationSequence() {
	t := suite.T()
	ctx := suite.Context
	fixture := fixtureCatalog(t)[0]
	_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "1.0.0", map[string]int64{"1.0.0": 2}))

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, releasesErr := snapshot.Releases(readCtx, nil, 1)
		return releasesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestReleasesRejectsMissingActivationSequence() {
	t := suite.T()
	ctx := suite.Context
	fixtures := fixtureCatalog(t)
	for _, fixture := range fixtures[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 3,
		"2.0.0": 4,
	}))

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, releasesErr := snapshot.Releases(readCtx, nil, 1)
		return releasesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestReleasesRejectsCorruptionOutsideReturnedPage() {
	t := suite.T()
	ctx := suite.Context
	fixtures := fixtureCatalog(t)
	for _, fixture := range fixtures[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))
	_, err := suite.Database.ExecContext(ctx, `
UPDATE schema_registry.releases
SET previous_manifest_digest = 'sha-256:0000000000000000000000000000000000000000000000000000000000000000'
WHERE schema_version = '1.1.0'`)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		// Only the newest item belongs to this page. The corrupt middle release
		// MUST still invalidate the complete externally supplied activation chain.
		items, releasesErr := snapshot.Releases(readCtx, nil, 1)
		require.Empty(t, items)
		return releasesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestIsAncestorRejectsPredecessorMismatch() {
	t := suite.T()
	ctx := suite.Context
	for _, fixture := range fixtureCatalog(t)[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))
	_, err := suite.Database.ExecContext(ctx, `
UPDATE schema_registry.release_lineage
SET predecessor_manifest_digest = 'sha-256:0000000000000000000000000000000000000000000000000000000000000000'
WHERE schema_version = '2.0.0'`)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, ancestryErr := snapshot.IsAncestor(readCtx, "1.0.0", "2.0.0")
		return ancestryErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestIsAncestorRejectsMissingLineageEvidence() {
	t := suite.T()
	ctx := suite.Context
	for _, fixture := range fixtureCatalog(t)[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))
	_, err := suite.Database.ExecContext(ctx, `DELETE FROM schema_registry.release_lineage WHERE schema_version = '2.0.0'`)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, ancestryErr := snapshot.IsAncestor(readCtx, "1.0.0", "2.0.0")
		return ancestryErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}

func (suite *readStoreSuite) TestIsAncestorRejectsDisconnectedBranchWithAscendingActivationSequence() {
	t := suite.T()
	ctx := suite.Context
	for _, fixture := range fixtureCatalog(t)[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))
	_, err := suite.Database.ExecContext(ctx, `
UPDATE schema_registry.releases
SET previous_schema_version = NULL, previous_manifest_digest = NULL
WHERE schema_version = '2.0.0'`)
	require.NoError(t, err)
	_, err = suite.Database.ExecContext(ctx, `
UPDATE schema_registry.release_lineage
SET chain_id = '00000000-0000-0000-0000-000000000002', chain_position = 1,
    predecessor_schema_version = NULL, predecessor_manifest_digest = NULL
WHERE schema_version = '2.0.0'`)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		isAncestor, ancestryErr := snapshot.IsAncestor(readCtx, "1.0.0", "2.0.0")
		require.NoError(t, ancestryErr)
		require.False(t, isAncestor)
		return nil
	})
	require.NoError(t, err)
}

func (suite *readStoreSuite) TestReadStoreVisibilityAndSnapshots() {
	t := suite.T()
	ctx := suite.Context
	database := suite.Database
	databaseURL := suite.DatabaseURL

	fixtures := fixtureCatalog(t)
	for _, fixture := range fixtures[:4] {
		_, err := loadValidatedFixture(t, ctx, database, fixture)
		require.NoError(t, err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{
		Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second,
	})
	require.NoError(t, err)

	err = store.Read(ctx, func(context.Context, *Snapshot) error { return nil })
	require.ErrorIs(t, err, ErrNoActiveRelease)

	require.NoError(t, prepopulateReadModelState(ctx, database, "1.1.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))

	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		require.Equal(t, "1.1.0", snapshot.Current.SchemaVersion)
		require.EqualValues(t, 3, snapshot.Current.SnapshotSequence)
		current, currentErr := snapshot.CurrentRelease(readCtx)
		require.NoError(t, currentErr)
		require.Equal(t, "1.1.0", current.SchemaVersion)
		releases, releaseErr := snapshot.Releases(readCtx, nil, 10)
		require.NoError(t, releaseErr)
		require.Len(t, releases, 3)
		for _, release := range releases {
			require.NotEqual(t, "2.1.0", release.SchemaVersion)
		}
		_, inactiveReleaseErr := snapshot.Release(readCtx, "2.1.0")
		require.ErrorIs(t, inactiveReleaseErr, ErrNotFound)
		firstPage, pageErr := snapshot.ReleasesAt(readCtx, nil, snapshot.Current.SnapshotSequence, 2)
		require.NoError(t, pageErr)
		require.Len(t, firstPage, 2)
		require.Equal(t, "2.0.0", firstPage[0].SchemaVersion)
		before := firstPage[1].ActivationSequence
		secondPage, pageErr := snapshot.ReleasesAt(readCtx, &before, snapshot.Current.SnapshotSequence, 2)
		require.NoError(t, pageErr)
		require.Len(t, secondPage, 1)
		require.Equal(t, "1.0.0", secondPage[0].SchemaVersion)
		selected, artifactErr := snapshot.ReleaseArtifact(readCtx, "1.1.0", "schema", "device")
		require.NoError(t, artifactErr)
		require.Equal(t, "rev-device-v1", selected.Revision)
		visible, artifactErr := snapshot.Artifact(readCtx, "rev-device-v1")
		require.NoError(t, artifactErr)
		require.Equal(t, selected, visible)
		inactive, artifactErr := snapshot.Artifact(readCtx, "rev-inactive-v1")
		require.ErrorIs(t, artifactErr, ErrNotFound)
		require.Empty(t, inactive)
		inactiveResources, resourcesErr := snapshot.Resources(readCtx, "rev-inactive-v1")
		require.ErrorIs(t, resourcesErr, ErrNotFound)
		require.Empty(t, inactiveResources)
		_, uriErr := snapshot.URIEntry(readCtx, "2.1.0", "https://registry.example.test/v1/resources/schema/inactive/rev-inactive-v1")
		require.ErrorIs(t, uriErr, ErrNotFound)
		inactiveStores, storesErr := snapshot.StorageEntries(readCtx, "2.1.0", "inactive")
		require.ErrorIs(t, storesErr, ErrNotFound)
		require.Empty(t, inactiveStores)
		history, historyErr := snapshot.ArtifactHistory(readCtx, "schema", "device", nil, snapshot.Current.SnapshotSequence, 10)
		require.NoError(t, historyErr)
		require.Len(t, history, 3)
		_, mappingErr := snapshot.SourceMappingByRevision(readCtx, "1.1.0", "missing-mapping")
		require.ErrorIs(t, mappingErr, ErrNotFound)
		return nil
	})
	require.NoError(t, err)

	// The manifest is authoritative for change summaries. A stale projected
	// column must not affect a release descriptor or collection item.
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.releases SET changes = '{"compatibility":"breaking"}'::jsonb WHERE schema_version = '1.1.0'`)
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		release, releaseErr := snapshot.Release(readCtx, "1.1.0")
		require.NoError(t, releaseErr)
		require.Empty(t, release.Changes)
		return nil
	})
	require.NoError(t, err)

	// A historical list item must validate its release projection, not only the
	// current release used for the response header.
	_, err = database.ExecContext(ctx, `SET session_replication_role = replica`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.releases SET source_repository = 'https://invalid.example.test' WHERE schema_version = '1.0.0'`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `SET session_replication_role = origin`)
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, releasesErr := snapshot.Releases(readCtx, nil, 10)
		return releasesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)

	_, err = database.ExecContext(ctx, `SET session_replication_role = replica`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.releases SET manifest_bytes = '{}'::bytea WHERE schema_version = '1.0.0'`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `SET session_replication_role = origin`)
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, releasesErr := snapshot.Releases(readCtx, nil, 10)
		return releasesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)

	_, err = database.ExecContext(ctx, `SET session_replication_role = replica`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.artifacts SET payload_bytes = 'fals'::bytea WHERE artifact_revision = 'rev-device-v1'`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `SET session_replication_role = origin`)
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, artifactErr := snapshot.ReleaseArtifact(readCtx, "1.1.0", "schema", "device")
		return artifactErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)

	_, err = database.ExecContext(ctx, `SET session_replication_role = replica`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.storage_catalog_entries SET store = 'telemetry-store' WHERE schema_version = '1.1.0' AND artifact_type = 'device'`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `SET session_replication_role = origin`)
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, entriesErr := snapshot.StorageEntries(readCtx, "1.1.0", "device")
		return entriesErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)

	// A cancelled request must release its pool connection for later callers.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = store.Read(cancelled, func(context.Context, *Snapshot) error { return nil })
	require.Error(t, err)
	require.Eventually(t, func() bool { return pool.Stat().AcquiredConns() == 0 }, time.Second, 10*time.Millisecond)
}

func (suite *readStoreSuite) TestArtifactHistoryRejectsCorruptHistoricalProjection() {
	t := suite.T()
	ctx := suite.Context
	for _, fixture := range fixtureCatalog(t)[:3] {
		_, err := loadValidatedFixture(t, ctx, suite.Database, fixture)
		require.NoError(t, err)
	}
	require.NoError(t, prepopulateReadModelState(ctx, suite.Database, "2.0.0", map[string]int64{
		"1.0.0": 1,
		"1.1.0": 2,
		"2.0.0": 3,
	}))

	_, err := suite.Database.ExecContext(ctx, `SET session_replication_role = replica`)
	require.NoError(t, err)
	_, err = suite.Database.ExecContext(ctx, `
UPDATE schema_registry.release_artifacts
SET artifact_revision = 'rev-location-v1'
WHERE schema_version = '1.0.0' AND kind = 'schema' AND artifact_type = 'device'`)
	require.NoError(t, err)
	_, err = suite.Database.ExecContext(ctx, `SET session_replication_role = origin`)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, suite.DatabaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store, err := NewStore(pool, Timeouts{
		Acquire: time.Second, Query: time.Second, Statement: time.Second, Lock: time.Second, IdleInTransaction: time.Second,
	})
	require.NoError(t, err)
	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		_, historyErr := snapshot.ArtifactHistory(readCtx, "schema", "device", nil, snapshot.Current.SnapshotSequence, 10)
		return historyErr
	})
	require.ErrorIs(t, err, ErrInconsistentState)
}
