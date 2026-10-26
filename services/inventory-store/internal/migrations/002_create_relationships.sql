-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE IF NOT EXISTS relationships (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id        UUID         NOT NULL REFERENCES assets(id),
    target_asset_id        UUID         NOT NULL REFERENCES assets(id),
    artifact_type          VARCHAR(64)  NOT NULL,
    source_system          VARCHAR(128) NOT NULL,
    source_key             TEXT,
    target_key             TEXT,
    source_iri             TEXT,
    target_iri             TEXT,
    extensions             JSONB        NOT NULL DEFAULT '{}',
    ingested_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source      TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    valid_from             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to               TIMESTAMPTZ
);

-- Partial unique index: only one open edge per logical endpoint pair + artifact_type.
-- source_key / target_key are stable "{source_system}:{source_id}" strings so the
-- constraint survives asset re-versioning (UUID changes but logical key does not).
CREATE UNIQUE INDEX IF NOT EXISTS ux_rel_logical_current
    ON relationships (source_key, target_key, artifact_type) WHERE valid_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_rel_source_asset  ON relationships(source_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_target_asset  ON relationships(target_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_artifact_type ON relationships(artifact_type);
CREATE INDEX IF NOT EXISTS idx_rel_valid_to       ON relationships(valid_to);

-- +goose Down
DROP TABLE IF EXISTS relationships;
