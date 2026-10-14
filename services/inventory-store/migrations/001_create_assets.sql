-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS assets (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_type           TEXT          NOT NULL,
    source_system           TEXT          NOT NULL,
    source_id               TEXT          NOT NULL,
    extensions              JSONB,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    created_at_source       TIMESTAMPTZ,
    last_updated_at_source  TIMESTAMPTZ,
    valid_from              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    valid_to                TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_assets_natural_key_current
    ON assets (source_system, source_id)
    WHERE valid_to IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_assets_artifact_type
    ON assets (artifact_type)
    WHERE valid_to IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_assets_created_at_id
    ON assets (created_at, id)
    WHERE valid_to IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS assets;
-- +goose StatementEnd
