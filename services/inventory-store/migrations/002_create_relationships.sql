-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS relationships (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id         UUID          NOT NULL REFERENCES assets(id),
    target_asset_id         UUID          NOT NULL REFERENCES assets(id),
    artifact_type           TEXT          NOT NULL,
    source_system           TEXT          NOT NULL,
    extensions              JSONB,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    created_at_source       TIMESTAMPTZ,
    last_updated_at_source  TIMESTAMPTZ,
    UNIQUE (source_asset_id, target_asset_id, artifact_type)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_relationships_source
    ON relationships (source_asset_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_relationships_target
    ON relationships (target_asset_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS relationships;
-- +goose StatementEnd
