// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "context"

// OutboxWriter persists a batch of normalized records to the durable outbox.
//
// Write MUST be atomic: a nil error means every row is durable; a non-nil error
// means no row is visible to any reader. Write MUST be a no-op (returning nil)
// for an empty or nil slice, and MUST be safe for concurrent use.
type OutboxWriter interface {
	Write(ctx context.Context, rows []OutboxRecord) error
}
