package postgres_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrationsSuite runs schema constraints and runtime privilege checks against a disposable migrated PostgreSQL database.
func TestMigrationsSuite(t *testing.T) {
	runPostgreSQLSuite(t, new(migrationsSuite))
}

// migrationsSuite verifies storage boundaries independently of application-level manifest validation.
type migrationsSuite struct {
	PostgreSQLSuite
}

// TestMigrationsAndBoundaries verifies row constraints, uniqueness, reference integrity, shared mapping sources, and read-only runtime privileges.
// It also verifies migrations provide no release-state transition routines or user-defined triggers.
func (suite *migrationsSuite) TestMigrationsAndBoundaries() {
	t := suite.T()
	ctx := suite.Context
	conn := suite.Database
	databaseURL := suite.DatabaseURL
	runtimePassword := randomPassword(t)
	_, err := conn.ExecContext(ctx, "CREATE ROLE schema_registry_runtime_test LOGIN PASSWORD "+quoteLiteral(runtimePassword))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `GRANT schema_registry_api TO schema_registry_runtime_test`)
	require.NoError(t, err)

	// Storage accepts structurally valid release metadata without interpreting its manifest payload.
	t.Run("valid boundary row", func(t *testing.T) {
		_, insertErr := conn.ExecContext(ctx, `
INSERT INTO schema_registry.releases (
    schema_version, manifest_digest, manifest_bytes, source_repository,
    source_commit, projection_commitment
) VALUES ($1, $2, $3, $4, $5, $6)`,
			"1.0.0", digest('a'), []byte("{}"), "https://example.test/repository",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", commitment())
		require.NoError(t, insertErr)
	})

	for _, test := range []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "empty logical type",
			query: `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('empty-type', 'schema', '', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/empty-type/empty-type', 'application/schema+json', $1, 0, '')`,
			args:  []any{digest('a')},
		},
		{
			name:  "logical type exceeds 255 bytes",
			query: `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ($1, 'schema', $2, '1.0.0', 'entity-type', $3, 'application/schema+json', $4, 0, '')`,
			args:  []any{"r1", strings.Repeat("a", 256), "https://registry.test/v1/resources/schema/a/r1", digest('b')},
		},
		{
			name:  "dot segment revision",
			query: `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('.', 'schema', 'device', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/device/x', 'application/schema+json', $1, 0, '')`,
			args:  []any{digest('c')},
		},
		{
			name:  "negative payload size",
			query: `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('r2', 'schema', 'device', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/device/r2', 'application/schema+json', $1, -1, '')`,
			args:  []any{digest('d')},
		},
		{
			name:  "over profile payload size",
			query: `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('too-large', 'schema', 'large', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/large/too-large', 'application/schema+json', $1, 16777217, repeat('x', 16777217)::bytea)`,
			args:  []any{digest('d')},
		},
		{
			name:  "unsafe resource name",
			query: `INSERT INTO schema_registry.artifact_resources (artifact_revision, name, role, media_type, digest, size, bytes) VALUES ('r3', '../resource', 'program', 'application/octet-stream', $1, 0, '')`,
			args:  []any{digest('e')},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, insertErr := conn.ExecContext(ctx, test.query, test.args...)
			require.Error(t, insertErr)
		})
	}

	// A release cannot select the same logical artifact identity twice.
	t.Run("duplicate release selection is rejected", func(t *testing.T) {
		insertArtifact(t, ctx, conn, "selection-revision", "selection", "https://registry.test/v1/resources/schema/selection/selection-revision")
		_, insertErr := conn.ExecContext(ctx, `INSERT INTO schema_registry.release_artifacts (schema_version, kind, artifact_type, artifact_revision) VALUES ('1.0.0', 'schema', 'selection', 'selection-revision')`)
		require.NoError(t, insertErr)
		_, err = conn.ExecContext(ctx, `INSERT INTO schema_registry.release_artifacts (schema_version, kind, artifact_type, artifact_revision) VALUES ('1.0.0', 'schema', 'selection', 'selection-revision')`)
		require.Error(t, err)
	})

	// Canonical artifact locations and activation sequences each identify only one stored record.
	t.Run("canonical URI and stored sequence are unique", func(t *testing.T) {
		insertArtifact(t, ctx, conn, "canonical-one", "canonical-one", "https://registry.test/v1/resources/schema/canonical-one/canonical")
		_, insertErr := conn.ExecContext(ctx, `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('canonical-two', 'schema', 'canonical-two', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/canonical-one/canonical', 'application/schema+json', $1, 0, '')`, digest('e'))
		require.Error(t, insertErr)
		_, insertErr = conn.ExecContext(ctx, `UPDATE schema_registry.releases SET activated_sequence = 1 WHERE schema_version = '1.0.0'`)
		require.NoError(t, insertErr)
		_, insertErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.releases (schema_version, manifest_digest, manifest_bytes, source_repository, source_commit, activated_sequence, projection_commitment) VALUES ('1.0.1', $1, '{}', 'https://example.test/repository', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1, $2)`, "sha-256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", commitment())
		require.Error(t, insertErr)
	})

	// The storage pointer accepts a stored release; application reads separately enforce activated visibility.
	t.Run("pre-populated current state accepts a stored release", func(t *testing.T) {
		_, insertErr := conn.ExecContext(ctx, `INSERT INTO schema_registry.releases (schema_version, manifest_digest, manifest_bytes, source_repository, source_commit, projection_commitment) VALUES ('9.0.0', $1, '{}', 'https://example.test/repository', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', $2)`, "sha-256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", commitment())
		require.NoError(t, insertErr)
		_, insertErr = conn.ExecContext(ctx, `UPDATE schema_registry.registry_state SET current_schema_version = '9.0.0' WHERE singleton = true`)
		require.NoError(t, insertErr)
	})

	// Migrations establish read-model storage without implementing activation workflows inside the database.
	t.Run("migration has no state-transition routines or triggers", func(t *testing.T) {
		var routineCount int
		require.NoError(t, conn.QueryRowContext(ctx, `
SELECT count(*)
FROM pg_catalog.pg_proc AS procedure
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
WHERE namespace.nspname = 'schema_registry'`).Scan(&routineCount))
		require.Zero(t, routineCount)

		var triggerCount int
		require.NoError(t, conn.QueryRowContext(ctx, `
SELECT count(*)
FROM pg_catalog.pg_trigger AS trigger
JOIN pg_catalog.pg_class AS relation ON relation.oid = trigger.tgrelid
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'schema_registry'
  AND NOT trigger.tgisinternal`).Scan(&triggerCount))
		require.Zero(t, triggerCount)
	})

	// A declared zero-length bytea value satisfies storage constraints independently of artifact format validation.
	t.Run("zero length payload is valid", func(t *testing.T) {
		_, insertErr := conn.ExecContext(ctx, `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ('empty-payload', 'schema', 'empty-payload', '1.0.0', 'entity-type', 'https://registry.test/v1/resources/schema/empty-payload/empty-payload', 'application/schema+json', $1, 0, '')`, digest('a'))
		require.NoError(t, insertErr)
	})

	// Mapping references must exist, but distinct mappings may share sources and repeat targets at different ordinals.
	t.Run("missing references fail and shared mapping sources are accepted", func(t *testing.T) {
		_, queryErr := conn.ExecContext(ctx, `INSERT INTO schema_registry.release_artifacts (schema_version, kind, artifact_type, artifact_revision) VALUES ('9.0.0', 'schema', 'missing', 'unknown-revision')`)
		require.Error(t, queryErr)
		insertArtifact(t, ctx, conn, "mapping-one", "mapping-one", "https://registry.test/v1/resources/schema/mapping-one/mapping-one")
		insertArtifact(t, ctx, conn, "mapping-two", "mapping-two", "https://registry.test/v1/resources/schema/mapping-two/mapping-two")
		insertArtifact(t, ctx, conn, "mapping-source", "mapping-source", "https://registry.test/v1/resources/schema/mapping-source/mapping-source")
		insertArtifact(t, ctx, conn, "mapping-target", "mapping-target", "https://registry.test/v1/resources/schema/mapping-target/mapping-target")
		insertResource(t, ctx, conn, "mapping-one", "program-one")
		insertResource(t, ctx, conn, "mapping-two", "program-two")
		for _, revision := range []string{"mapping-one", "mapping-two", "mapping-source", "mapping-target"} {
			_, queryErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.release_artifacts (schema_version,kind,artifact_type,artifact_revision) VALUES ('9.0.0','schema',$1,$1)`, revision)
			require.NoError(t, queryErr)
		}
		for _, revision := range []string{"mapping-one", "mapping-two"} {
			_, queryErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.source_mapping_details (schema_version,mapping_revision,source_artifact_type,source_revision,target_schema_version,engine_language,engine_language_version,execution_profile) VALUES ('9.0.0',$1,'mapping-source','mapping-source','9.0.0','language','1.0.0','profile')`, revision)
			require.NoError(t, queryErr)
		}
		for ordinal := range 2 {
			_, queryErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.source_mapping_transformations (schema_version,mapping_revision,ordinal,target_artifact_type,target_revision,cardinality,program_name,program_digest) VALUES ('9.0.0','mapping-one',$1,'mapping-target','mapping-target','one','program-one',$2)`, ordinal, digest('a'))
			require.NoError(t, queryErr)
		}
		_, queryErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.source_mapping_details (schema_version,mapping_revision,source_artifact_type,source_revision,target_schema_version,engine_language,engine_language_version,execution_profile) VALUES ('9.0.0','mapping-one','mapping-source','mapping-source','9.0.0','language','1.0.0','profile')`)
		require.Error(t, queryErr)
		_, queryErr = conn.ExecContext(ctx, `UPDATE schema_registry.source_mapping_details SET condition_program_name = 'missing', condition_program_digest = $1 WHERE schema_version = '9.0.0' AND mapping_revision = 'mapping-two'`, digest('a'))
		require.Error(t, queryErr)
		for _, invalid := range []struct {
			ordinal                      int
			target, cardinality, program string
		}{
			{0, "mapping-target", "one", "program-one"},
			{2, "missing", "one", "program-one"},
			{2, "mapping-target", "zero", "program-one"},
			{2, "mapping-target", "one", "missing"},
			{-1, "mapping-target", "one", "program-one"},
			{10000, "mapping-target", "one", "program-one"},
		} {
			_, queryErr = conn.ExecContext(ctx, `INSERT INTO schema_registry.source_mapping_transformations (schema_version,mapping_revision,ordinal,target_artifact_type,target_revision,cardinality,program_name,program_digest) VALUES ('9.0.0','mapping-one',$1,'mapping-target',$2,$3,$4,$5)`, invalid.ordinal, invalid.target, invalid.cardinality, invalid.program, digest('a'))
			require.Error(t, queryErr)
		}
	})

	// The API role reads projected mapping tables but cannot mutate mapping or singleton state rows.
	t.Run("runtime role is read only", func(t *testing.T) {
		_, roleErr := conn.ExecContext(ctx, "SET ROLE schema_registry_api")
		require.NoError(t, roleErr)
		for _, table := range []string{"source_mapping_details", "source_mapping_transformations"} {
			// These identifiers are a fixed test-owned allowlist, not external input.
			// #nosec G202 -- table values come from the fixed test-owned allowlist.
			_, roleErr = conn.ExecContext(ctx, "SELECT * FROM schema_registry."+table+" LIMIT 0")
			require.NoError(t, roleErr)
			// #nosec G202 -- table values come from the fixed test-owned allowlist.
			_, roleErr = conn.ExecContext(ctx, "DELETE FROM schema_registry."+table+" WHERE false")
			require.Error(t, roleErr)
		}
		_, roleErr = conn.ExecContext(ctx, "INSERT INTO schema_registry.registry_state (singleton) VALUES (false)")
		require.Error(t, roleErr)
		_, roleErr = conn.ExecContext(ctx, "RESET ROLE")
		require.NoError(t, roleErr)
	})

	// A runtime login cannot create temporary, registry, or migration tables or perform any registry mutation.
	t.Run("runtime login assumes only read role", func(t *testing.T) {
		runtimeURL := replaceDatabaseUser(databaseURL, "schema_registry_runtime_test", runtimePassword)
		runtimeConn, openErr := sql.Open("pgx", runtimeURL)
		require.NoError(t, openErr)
		t.Cleanup(func() { require.NoError(t, runtimeConn.Close()) })
		_, roleErr := runtimeConn.ExecContext(ctx, "SET ROLE schema_registry_api")
		require.NoError(t, roleErr)
		for _, operation := range []string{
			"CREATE TEMPORARY TABLE denied_temp (id integer)",
			"CREATE TABLE schema_registry.denied_ddl (id integer)",
			"CREATE TABLE goose_db_version (id integer)",
			"INSERT INTO schema_registry.registry_state (singleton) VALUES (false)",
			"UPDATE schema_registry.registry_state SET current_schema_version = NULL",
			"DELETE FROM schema_registry.registry_state",
			"TRUNCATE schema_registry.registry_state",
		} {
			_, operationErr := runtimeConn.ExecContext(ctx, operation)
			require.Error(t, operationErr, operation)
		}
	})

	_, err = conn.ExecContext(ctx, `DROP ROLE schema_registry_runtime_test`)
	require.NoError(t, err)
}

// digest supplies a syntactically valid fixed digest for constraint tests, not byte-integrity verification.
// The ignored argument keeps fixture call sites distinct without implying different digest values.
func digest(_ byte) string {
	return "sha-256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

// commitment supplies the required commitment length without asserting authoritative projection integrity.
func commitment() []byte { return make([]byte, 32) }

// insertArtifact supplies a structurally valid schema row for uniqueness, reference, and privilege checks.
func insertArtifact(t *testing.T, ctx context.Context, conn *sql.DB, revision, artifactType, uri string) {
	t.Helper()
	_, err := conn.ExecContext(ctx, `INSERT INTO schema_registry.artifacts (artifact_revision, kind, artifact_type, created_under_schema_version, schema_class, canonical_uri, payload_media_type, payload_digest, payload_size, payload_bytes) VALUES ($1, 'schema', $2, '1.0.0', 'entity-type', $3, 'application/schema+json', $4, 0, '')`, revision, artifactType, uri, digest('a'))
	require.NoError(t, err)
}

// insertResource supplies an empty program resource so mapping foreign-key tests have an existing target.
func insertResource(t *testing.T, ctx context.Context, conn *sql.DB, revision, name string) {
	t.Helper()
	_, err := conn.ExecContext(ctx, `INSERT INTO schema_registry.artifact_resources (artifact_revision, name, role, media_type, digest, size, bytes) VALUES ($1, $2, 'program', 'application/octet-stream', $3, 0, '')`, revision, name, digest('a'))
	require.NoError(t, err)
}

// replaceDatabaseUser builds a test login URL while preserving the disposable database's endpoint and options.
// It panics if the test-supplied URL is malformed.
func replaceDatabaseUser(databaseURL, user, password string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		panic(err)
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String()
}

// randomPassword generates an ephemeral runtime-test credential instead of embedding a reusable secret.
func randomPassword(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// quoteLiteral escapes a test-generated role password for DDL, where PostgreSQL does not accept parameter placeholders.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
