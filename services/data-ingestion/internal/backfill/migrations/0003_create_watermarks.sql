-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
--
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE IF NOT EXISTS data_ingestion_watermarks (
    connector_id   TEXT        NOT NULL,
    partition_key  TEXT        NOT NULL,
    watermark      JSONB,
    gap_open       BOOLEAN     NOT NULL DEFAULT FALSE,
    live_frontier  JSONB,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (connector_id, partition_key)
);

-- +goose Down
DROP TABLE IF EXISTS data_ingestion_watermarks;
