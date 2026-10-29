// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package backfill provides the watermark store and gap state machine for the
// Data Ingestion service. It tracks per-(connector, partition) ingestion
// position and controls which channel — live stream or backfill — may advance
// the committed watermark at any given time.
package backfill
