-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
--
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE SCHEMA IF NOT EXISTS schema_registry;

CREATE TABLE schema_registry.releases (
    schema_version text COLLATE "C" PRIMARY KEY,
    manifest_digest text COLLATE "C" NOT NULL UNIQUE,
    manifest_bytes bytea NOT NULL,
    previous_schema_version text COLLATE "C",
    previous_manifest_digest text COLLATE "C",
    source_repository text COLLATE "C" NOT NULL,
    source_commit text COLLATE "C" NOT NULL,
    changes jsonb,
    activated_sequence bigint UNIQUE,
    projection_commitment bytea NOT NULL,
    CHECK (schema_version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'),
    CHECK (manifest_digest ~ '^sha-256:[0-9a-f]{64}$'),
    CHECK (octet_length(manifest_bytes) > 0 AND octet_length(manifest_bytes) <= 16777216),
    CHECK ((previous_schema_version IS NULL) = (previous_manifest_digest IS NULL)),
    CHECK (previous_manifest_digest IS NULL OR previous_manifest_digest ~ '^sha-256:[0-9a-f]{64}$'),
    CHECK (source_repository ~ '^[A-Za-z][A-Za-z0-9+.-]*:'),
    CHECK (source_commit ~ '^[0-9a-f]{40}([0-9a-f]{24})?$'),
    CHECK (activated_sequence IS NULL OR activated_sequence > 0),
    CHECK (octet_length(projection_commitment) = 32),
    FOREIGN KEY (previous_schema_version) REFERENCES schema_registry.releases(schema_version)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX releases_activated_sequence_index
    ON schema_registry.releases (activated_sequence DESC)
    WHERE activated_sequence IS NOT NULL;

CREATE TABLE schema_registry.registry_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    current_schema_version text COLLATE "C",
    FOREIGN KEY (current_schema_version) REFERENCES schema_registry.releases(schema_version)
        DEFERRABLE INITIALLY DEFERRED
);
INSERT INTO schema_registry.registry_state (singleton, current_schema_version) VALUES (true, NULL);

CREATE TABLE schema_registry.artifacts (
    artifact_revision text COLLATE "C" PRIMARY KEY,
    kind text COLLATE "C" NOT NULL,
    artifact_type text COLLATE "C" NOT NULL,
    created_under_schema_version text COLLATE "C" NOT NULL,
    schema_class text COLLATE "C",
    target_schema_version text COLLATE "C",
    canonical_uri text COLLATE "C" NOT NULL UNIQUE,
    payload_media_type text COLLATE "C" NOT NULL,
    payload_digest text COLLATE "C" NOT NULL,
    payload_size bigint NOT NULL,
    payload_bytes bytea NOT NULL,
    extensions jsonb,
    CHECK (artifact_revision <> '.' AND artifact_revision <> '..' AND artifact_revision ~ '^[A-Za-z0-9._~-]{1,255}$'),
    CHECK (kind IN ('schema', 'context', 'source-mapping')),
    CHECK (artifact_type ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND octet_length(artifact_type) <= 255),
    CHECK (schema_class IS NULL OR schema_class IN ('entity-type', 'identifier-type', 'edge-type', 'source-record')),
    CHECK ((kind = 'schema') = (schema_class IS NOT NULL)),
    CHECK ((kind = 'source-mapping') = (target_schema_version IS NOT NULL)),
    CHECK (payload_media_type <> '' AND octet_length(payload_media_type) <= 255),
    CHECK (payload_digest ~ '^sha-256:[0-9a-f]{64}$'),
    CHECK (payload_size >= 0 AND payload_size <= 16777216),
    CHECK (payload_size = octet_length(payload_bytes)),
    CHECK (octet_length(payload_bytes) <= 16777216),
    CHECK (octet_length(canonical_uri) <= 8192 AND canonical_uri ~ '^[A-Za-z][A-Za-z0-9+.-]*:'),
    FOREIGN KEY (created_under_schema_version) REFERENCES schema_registry.releases(schema_version)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX artifacts_identity_index ON schema_registry.artifacts (kind, artifact_type);

CREATE TABLE schema_registry.artifact_resources (
    artifact_revision text COLLATE "C" NOT NULL,
    name text COLLATE "C" NOT NULL,
    role text COLLATE "C" NOT NULL,
    media_type text COLLATE "C" NOT NULL,
    digest text COLLATE "C" NOT NULL,
    size bigint NOT NULL,
    bytes bytea NOT NULL,
    PRIMARY KEY (artifact_revision, name),
    CHECK (name <> '.' AND name <> '..' AND name ~ '^[A-Za-z0-9._~-]{1,255}$'),
    CHECK (role ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CHECK (media_type <> '' AND octet_length(media_type) <= 255),
    CHECK (digest ~ '^sha-256:[0-9a-f]{64}$'),
    CHECK (size >= 0 AND size <= 16777216 AND size = octet_length(bytes)),
    CHECK (octet_length(bytes) <= 16777216),
    FOREIGN KEY (artifact_revision) REFERENCES schema_registry.artifacts(artifact_revision)
        ON DELETE RESTRICT
);

CREATE TABLE schema_registry.artifact_dependencies (
    artifact_revision text COLLATE "C" NOT NULL,
    dependency_kind text COLLATE "C" NOT NULL,
    dependency_artifact_type text COLLATE "C" NOT NULL,
    dependency_revision text COLLATE "C" NOT NULL,
    PRIMARY KEY (artifact_revision, dependency_kind, dependency_artifact_type, dependency_revision),
    CHECK (dependency_kind IN ('schema', 'context', 'source-mapping')),
    CHECK (dependency_artifact_type ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND octet_length(dependency_artifact_type) <= 255),
    CHECK (dependency_revision <> '.' AND dependency_revision <> '..' AND dependency_revision ~ '^[A-Za-z0-9._~-]{1,255}$'),
    FOREIGN KEY (artifact_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (dependency_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT
);
CREATE INDEX artifact_dependencies_target_index
    ON schema_registry.artifact_dependencies (dependency_revision, artifact_revision);

CREATE TABLE schema_registry.release_artifacts (
    schema_version text COLLATE "C" NOT NULL,
    kind text COLLATE "C" NOT NULL,
    artifact_type text COLLATE "C" NOT NULL,
    artifact_revision text COLLATE "C" NOT NULL,
    PRIMARY KEY (schema_version, kind, artifact_type),
    UNIQUE (schema_version, artifact_revision),
    CHECK (kind IN ('schema', 'context', 'source-mapping')),
    CHECK (artifact_type ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND octet_length(artifact_type) <= 255),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT,
    FOREIGN KEY (artifact_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT
);
CREATE INDEX release_artifacts_revision_index ON schema_registry.release_artifacts (artifact_revision, schema_version);

CREATE TABLE schema_registry.schema_context_bindings (
    schema_version text COLLATE "C" NOT NULL,
    schema_revision text COLLATE "C" NOT NULL,
    context_revision text COLLATE "C" NOT NULL,
    PRIMARY KEY (schema_version, schema_revision, context_revision),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT,
    FOREIGN KEY (schema_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (context_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT
);
CREATE INDEX schema_context_bindings_schema_index
    ON schema_registry.schema_context_bindings (schema_version, schema_revision, context_revision);

CREATE TABLE schema_registry.uri_catalog_entries (
    schema_version text COLLATE "C" NOT NULL,
    uri text COLLATE "C" NOT NULL,
    artifact_revision text COLLATE "C" NOT NULL,
    resource_name text COLLATE "C",
    PRIMARY KEY (schema_version, uri),
    CHECK (octet_length(uri) <= 8192 AND uri ~ '^[A-Za-z][A-Za-z0-9+.-]*:'),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT,
    FOREIGN KEY (artifact_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (artifact_revision, resource_name) REFERENCES schema_registry.artifact_resources(artifact_revision, name)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE schema_registry.storage_catalog_entries (
    schema_version text COLLATE "C" NOT NULL,
    artifact_type text COLLATE "C" NOT NULL,
    store text COLLATE "C" NOT NULL,
    PRIMARY KEY (schema_version, artifact_type, store),
    CHECK (artifact_type ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND octet_length(artifact_type) <= 255),
    CHECK (store IN ('data-center-inventory', 'telemetry-store', 'insights-repository', 'recorded-actions-repository')),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT
);
CREATE INDEX storage_catalog_entries_lookup_index
    ON schema_registry.storage_catalog_entries (schema_version, artifact_type, store);

CREATE TABLE schema_registry.source_mapping_details (
    schema_version text COLLATE "C" NOT NULL,
    mapping_revision text COLLATE "C" PRIMARY KEY,
    source_artifact_type text COLLATE "C" NOT NULL,
    source_revision text COLLATE "C" NOT NULL,
    target_artifact_type text COLLATE "C" NOT NULL,
    target_revision text COLLATE "C" NOT NULL,
    target_schema_version text COLLATE "C" NOT NULL,
    cardinality text COLLATE "C" NOT NULL,
    engine_language text COLLATE "C" NOT NULL,
    engine_language_version text COLLATE "C" NOT NULL,
    execution_profile text COLLATE "C" NOT NULL,
    program_name text COLLATE "C" NOT NULL,
    program_digest text COLLATE "C" NOT NULL,
    UNIQUE (schema_version, source_artifact_type),
    CHECK (cardinality IN ('one', 'many')),
    CHECK (engine_language <> '' AND engine_language_version <> '' AND execution_profile <> ''),
    CHECK (program_digest ~ '^sha-256:[0-9a-f]{64}$'),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT,
    FOREIGN KEY (mapping_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (source_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (target_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    FOREIGN KEY (mapping_revision, program_name) REFERENCES schema_registry.artifact_resources(artifact_revision, name)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX source_mapping_details_lookup_index
    ON schema_registry.source_mapping_details (schema_version, source_artifact_type, target_artifact_type);

CREATE TABLE schema_registry.release_projection_commitments (
    schema_version text COLLATE "C" NOT NULL,
    family text COLLATE "C" NOT NULL,
    commitment bytea NOT NULL,
    PRIMARY KEY (schema_version, family),
    CHECK (family IN ('release', 'artifact', 'resource', 'dependency', 'binding', 'uri', 'storage', 'source-mapping')),
    CHECK (octet_length(commitment) = 32),
    FOREIGN KEY (schema_version) REFERENCES schema_registry.releases(schema_version) ON DELETE RESTRICT
);

CREATE TABLE schema_registry.activated_revision_reachability (
    artifact_revision text COLLATE "C" PRIMARY KEY,
    first_activated_sequence bigint NOT NULL,
    FOREIGN KEY (artifact_revision) REFERENCES schema_registry.artifacts(artifact_revision) ON DELETE RESTRICT,
    CHECK (first_activated_sequence > 0)
);

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'schema_registry_api') THEN
        CREATE ROLE schema_registry_api NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON SCHEMA schema_registry FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA schema_registry FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA schema_registry FROM PUBLIC;
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());
    EXECUTE format('REVOKE ALL ON DATABASE %I FROM schema_registry_api', current_database());
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO schema_registry_api', current_database());
END;
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA schema_registry TO schema_registry_api;
GRANT SELECT ON ALL TABLES IN SCHEMA schema_registry TO schema_registry_api;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA schema_registry FROM schema_registry_api;
ALTER DEFAULT PRIVILEGES IN SCHEMA schema_registry REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES IN SCHEMA schema_registry GRANT SELECT ON TABLES TO schema_registry_api;
ALTER DEFAULT PRIVILEGES IN SCHEMA schema_registry REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

-- +goose Down
DROP SCHEMA IF EXISTS schema_registry CASCADE;
-- Keep the NOLOGIN role because external deployment provisioning can retain
-- membership grants for the secret-backed runtime login across rollbacks.
