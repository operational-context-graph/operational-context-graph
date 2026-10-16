// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package outbox implements the persistent PostgreSQL outbox: the concrete
// OutboxWriter the ingest pipeline writes to, and the embedded schema migrations.
package outbox

import "embed"

// migrationsFS holds the embedded SQL migrations applied by Migrate.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS
