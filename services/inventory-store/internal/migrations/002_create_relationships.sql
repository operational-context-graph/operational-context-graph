-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE IF NOT EXISTS relationships (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id        UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    target_asset_id        UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    artifact_type          VARCHAR(64)  NOT NULL,
    source_system          VARCHAR(128) NOT NULL,
    extensions             JSONB,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at_source      TIMESTAMPTZ,
    last_updated_at_source TIMESTAMPTZ,
    UNIQUE (source_asset_id, target_asset_id, artifact_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_source_asset  ON relationships(source_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_target_asset  ON relationships(target_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_artifact_type ON relationships(artifact_type);

-- +goose Down
DROP TABLE IF EXISTS relationships;
