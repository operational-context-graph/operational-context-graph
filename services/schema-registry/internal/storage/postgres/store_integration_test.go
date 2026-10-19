// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestReadStoreSuite(t *testing.T) {
	runPostgreSQLSuite(t, new(readStoreSuite))
}

type readStoreSuite struct {
	PostgreSQLSuite
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
		current, currentErr := snapshot.CurrentRelease(readCtx)
		require.NoError(t, currentErr)
		require.Equal(t, "1.1.0", current.SchemaVersion)
		releases, releaseErr := snapshot.Releases(readCtx, nil, 10)
		require.NoError(t, releaseErr)
		require.Len(t, releases, 3)
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
		history, historyErr := snapshot.ArtifactHistory(readCtx, "schema", "device", nil, 10)
		require.NoError(t, historyErr)
		require.Len(t, history, 3)
		return nil
	})
	require.NoError(t, err)

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
