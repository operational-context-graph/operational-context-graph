-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- Doris user setup for OCG — local dev
-- Apply after doris-init.sql once Doris is healthy.

CREATE USER IF NOT EXISTS 'ocg_writer'@'%' IDENTIFIED BY 'dev-password-not-for-production';
GRANT LOAD_PRIV ON ocg.* TO 'ocg_writer'@'%';

CREATE USER IF NOT EXISTS 'ocg_reader'@'%' IDENTIFIED BY 'dev-password-not-for-production';
GRANT SELECT_PRIV ON ocg.* TO 'ocg_reader'@'%';
