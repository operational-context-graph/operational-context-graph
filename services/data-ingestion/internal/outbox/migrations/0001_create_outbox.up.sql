-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
--
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE IF NOT EXISTS outbox (
    id                    BIGSERIAL PRIMARY KEY,
    target_store          TEXT        NOT NULL,
    logical_artifact_type TEXT        NOT NULL,
    schema_version        TEXT        NOT NULL,
    payload               JSONB       NOT NULL,
    content_type          TEXT        NOT NULL DEFAULT 'application/json',
    batch_id              BIGINT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pending rows for a store, in outbox order (drain scan).
CREATE INDEX IF NOT EXISTS outbox_pending_idx ON outbox (target_store, id) WHERE batch_id IS NULL;

-- Resend and delete by pinned batch.
CREATE INDEX IF NOT EXISTS outbox_batch_id_idx ON outbox (batch_id);
