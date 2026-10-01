-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- PostgreSQL schema for OCG Inventory Store — local dev
-- replication_num equivalent: n/a (Postgres handles replication server-side)

CREATE TABLE IF NOT EXISTS assets (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_type   VARCHAR(64)  NOT NULL,
    source       VARCHAR(128) NOT NULL,
    external_id  VARCHAR(256) NOT NULL,
    name         VARCHAR(256),
    ip_addresses JSONB,
    attributes   JSONB,
    ingested_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (source, external_id)
);

CREATE INDEX IF NOT EXISTS idx_assets_type   ON assets(asset_type);
CREATE INDEX IF NOT EXISTS idx_assets_source ON assets(source);
CREATE INDEX IF NOT EXISTS idx_assets_name   ON assets(name);
CREATE INDEX IF NOT EXISTS idx_assets_cursor ON assets(ingested_at, id);

CREATE TABLE IF NOT EXISTS relationships (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    source_asset_id   UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    target_asset_id   UUID         NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    relationship_type VARCHAR(64)  NOT NULL,
    source            VARCHAR(128) NOT NULL,
    attributes        JSONB,
    ingested_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (source_asset_id, target_asset_id, relationship_type)
);

CREATE INDEX IF NOT EXISTS idx_rel_source_asset ON relationships(source_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_target_asset ON relationships(target_asset_id);
CREATE INDEX IF NOT EXISTS idx_rel_type         ON relationships(relationship_type);

CREATE TABLE IF NOT EXISTS asset_history (
    id          BIGSERIAL      PRIMARY KEY,
    asset_id    UUID           NOT NULL REFERENCES assets(id),
    changed_at  TIMESTAMPTZ(3) NOT NULL,
    changed_by  TEXT           NOT NULL,
    operation   TEXT           NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
    snapshot    JSONB          NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_asset_history_asset_id   ON asset_history(asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_history_changed_at ON asset_history(changed_at DESC);

CREATE OR REPLACE FUNCTION fn_asset_history() RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO asset_history (asset_id, changed_at, changed_by, operation, snapshot)
    VALUES (
        COALESCE(OLD.id, NEW.id),
        clock_timestamp(),
        current_user,
        TG_OP,
        CASE TG_OP WHEN 'DELETE' THEN row_to_json(OLD) ELSE row_to_json(NEW) END
    );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_asset_history ON assets;
CREATE TRIGGER trg_asset_history
AFTER INSERT OR UPDATE OR DELETE ON assets
FOR EACH ROW EXECUTE FUNCTION fn_asset_history();
