-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE IF NOT EXISTS assets (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    iri                    TEXT,
    artifact_type          VARCHAR(64)  NOT NULL,
    source_system          VARCHAR(128) NOT NULL,
    source_id              VARCHAR(256) NOT NULL,
    extensions             JSONB        NOT NULL DEFAULT '{}',
    ingested_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source      TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    valid_from             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to               TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_assets_natural_current
    ON assets (source_system, source_id) WHERE valid_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_assets_artifact_type ON assets(artifact_type);
CREATE INDEX IF NOT EXISTS idx_assets_source_system ON assets(source_system);
CREATE INDEX IF NOT EXISTS idx_assets_cursor        ON assets(created_at, id);

-- +goose Down
DROP TABLE IF EXISTS assets;
