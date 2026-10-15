-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- PostgreSQL schema for OCG Inventory Store — local dev

CREATE TABLE IF NOT EXISTS assets (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_type         VARCHAR(64)  NOT NULL,
    source_system         VARCHAR(128) NOT NULL,
    source_id             VARCHAR(256) NOT NULL,
    extensions            JSONB,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source     TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    valid_from            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_to              TIMESTAMPTZ,
    UNIQUE (source_system, source_id, valid_from)
);

CREATE INDEX IF NOT EXISTS idx_assets_artifact_type  ON assets(artifact_type);
CREATE INDEX IF NOT EXISTS idx_assets_source_system  ON assets(source_system);
CREATE INDEX IF NOT EXISTS idx_assets_natural_key    ON assets(source_system, source_id) WHERE valid_to IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_cursor         ON assets(created_at, id);

CREATE TABLE IF NOT EXISTS relationships (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id       UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    target_asset_id       UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    artifact_type         VARCHAR(64)  NOT NULL,
    source_system         VARCHAR(128) NOT NULL,
    extensions            JSONB,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source     TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    UNIQUE (source_asset_id, target_asset_id, artifact_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_source_asset ON relationships(source_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_target_asset ON relationships(target_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_artifact_type ON relationships(artifact_type);
