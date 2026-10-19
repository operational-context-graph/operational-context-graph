// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

	admin, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close(ctx)) })
	require.NoError(t, activateTestRelease(ctx, admin, "1.0.0", nil))
	previous := "1.0.0"
	require.NoError(t, activateTestRelease(ctx, admin, "1.1.0", &previous))
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.registry_state SET current_schema_version = '1.0.0' WHERE singleton = true`)
	require.Error(t, err)
	_, err = database.ExecContext(ctx, `UPDATE schema_registry.registry_state SET current_schema_version = NULL WHERE singleton = true`)
	require.Error(t, err)

	err = store.Read(ctx, func(readCtx context.Context, snapshot *Snapshot) error {
		require.Equal(t, "1.1.0", snapshot.Current.SchemaVersion)
		current, currentErr := snapshot.CurrentRelease(readCtx)
		require.NoError(t, currentErr)
		require.Equal(t, "1.1.0", current.SchemaVersion)
		releases, releaseErr := snapshot.Releases(readCtx, nil, 10)
		require.NoError(t, releaseErr)
		require.Len(t, releases, 2)
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
		require.Len(t, history, 2)
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

func (suite *readStoreSuite) TestConcurrentActivationHasOneWinner() {
	t := suite.T()
	ctx := suite.Context
	database := suite.Database
	databaseURL := suite.DatabaseURL
	fixtures := fixtureCatalog(t)
	for _, fixture := range fixtures[:2] {
		_, err := loadValidatedFixture(t, ctx, database, fixture)
		require.NoError(t, err)
	}
	first, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close(ctx)) })
	second, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close(ctx)) })
	require.NoError(t, activateTestRelease(ctx, first, "1.0.0", nil))
	previous := "1.0.0"
	injectedFailure := errors.New("injected activation failure")
	err = activateTestReleaseWithFailure(ctx, first, "1.1.0", &previous, func(pgx.Tx) error { return injectedFailure })
	require.ErrorIs(t, err, injectedFailure)
	var failedSequence *int64
	require.NoError(t, database.QueryRowContext(ctx, `SELECT activated_sequence FROM schema_registry.releases WHERE schema_version = '1.1.0'`).Scan(&failedSequence))
	require.Nil(t, failedSequence)
	var current string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT current_schema_version FROM schema_registry.registry_state WHERE singleton`).Scan(&current))
	require.Equal(t, "1.0.0", current)
	errors := make(chan error, 2)
	go func() { errors <- activateTestRelease(ctx, first, "1.1.0", &previous) }()
	go func() { errors <- activateTestRelease(ctx, second, "1.1.0", &previous) }()
	firstResult := <-errors
	secondResult := <-errors
	require.True(t, (firstResult == nil) != (secondResult == nil))
	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT count(*) FROM schema_registry.activated_revision_reachability WHERE artifact_revision = 'rev-location-v1'`).Scan(&count))
	require.Equal(t, 1, count)
}
