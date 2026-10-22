// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package store defines the shared contract between the outbox drain and the
// concrete store writers.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// StoreID is the destination store a batch is routed to. It aliases ingest.StoreID.
type StoreID = ingest.StoreID

// BatchLimits reports a store's batch thresholds. The drain closes a batch when
// the record count MaxRecords, the byte size MaxBytes, or the linger time
// MaxLinger is first reached. MaxBytes of 0 disables the byte trigger.
type BatchLimits struct {
	MaxRecords int
	MaxBytes   int64
	MaxLinger  time.Duration
}

// Validate reports whether the limits are usable by the drain. MaxRecords and
// MaxLinger must be positive (a zero record count would never flush on size and
// a zero linger would never bound low-traffic latency). MaxBytes must not be
// negative; 0 means the byte trigger is disabled.
func (l BatchLimits) Validate() error {
	if l.MaxRecords <= 0 {
		return fmt.Errorf("%w: MaxRecords must be > 0, got %d", ErrInvalidLimits, l.MaxRecords)
	}
	if l.MaxLinger <= 0 {
		return fmt.Errorf("%w: MaxLinger must be > 0, got %s", ErrInvalidLimits, l.MaxLinger)
	}
	if l.MaxBytes < 0 {
		return fmt.Errorf("%w: MaxBytes must be >= 0, got %d", ErrInvalidLimits, l.MaxBytes)
	}
	return nil
}

// Record is one canonical record in a batch handed to a store writer.
type Record struct {
	Schema      ingest.SchemaKey
	Payload     []byte
	ContentType string
}

// Batch is one immutable unit of delivery. ID is the pinned batch_id from the outbox;
// it is stable across retries and restarts.
type Batch struct {
	ID      int64
	Records []Record
}

// StoreWriter is implemented for each store.
//
// Implementations MUST be safe for concurrent use. Write delivers one immutable
// batch: a nil error means the store durably accepted the whole batch (there is
// no per-record partial result). Callers distinguish failures with
// errors.Is(err, ErrOverloaded) and errors.As(err, *PermanentError); any other
// non-nil error is treated as transient back-pressure (pause and retry).
type StoreWriter interface {
	// StoreID is the destination this store serves. It MUST match the outbox
	// target_store value and the Schema Registry's target-store mapping.
	StoreID() StoreID
	// Write delivers one immutable batch.
	Write(ctx context.Context, batch Batch) error
	// Limits reports this store's batch thresholds.
	Limits() BatchLimits
}
