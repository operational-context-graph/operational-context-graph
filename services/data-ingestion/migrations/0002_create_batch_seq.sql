-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
--
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Dedicated sequence that assigns batch_id values when the drain pins a batch.
-- The drain reserves one value per batch and stamps it on every row of that
-- batch, so the persisted batch_id is a stable idempotency key across retries
-- and restarts. The sequence is additive: it does not touch the outbox table or
-- its indexes.
CREATE SEQUENCE IF NOT EXISTS data_ingestion_outbox_batch_seq AS BIGINT;

-- +goose Down
DROP SEQUENCE IF EXISTS data_ingestion_outbox_batch_seq;
