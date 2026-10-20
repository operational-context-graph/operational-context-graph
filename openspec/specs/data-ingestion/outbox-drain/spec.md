# data-ingestion/outbox-drain Specification

## Purpose
The outbox drain is the asynchronous delivery half of Data Ingestion. It moves durable outbox rows to the stores that own them with at-least-once delivery, immutable batches, per-store back-pressure, and restart durability, so connectors stay isolated from store availability (ADR-013).

## Requirements

### Requirement: Per-store drain workers with isolation

The drain SHALL run as a single process with one worker per `target_store`. Each worker SHALL scan, batch, pin, deliver, and delete only rows whose `target_store` matches the `StoreID` it serves. A stall, pause, or back-off in one worker SHALL NOT delay any other worker. Workers SHALL NOT share mutable in-process delivery state.

#### Scenario: Each registered store gets an independent worker

- **WHEN** two stores `telemetry` and `inventory` are registered and both have pending rows
- **THEN** each store's rows are delivered by its own worker
- **AND** neither worker waits on the other to make progress

#### Scenario: One store stalling does not block another (concurrent isolation)

- **WHEN** the `telemetry` worker is paused in back-off on an overloaded store while the `inventory` worker runs at the same time
- **THEN** the `inventory` worker keeps forming, delivering, and deleting its batches
- **AND** the two workers run without data races (verified under `-race`)

#### Scenario: Two workers pin disjoint rows concurrently

- **WHEN** the `telemetry` and `inventory` workers scan and pin at the same instant against the same outbox table
- **THEN** each worker pins only rows carrying its own `target_store`
- **AND** no row is pinned by both workers

#### Scenario: Zero registered stores is a no-op

- **WHEN** the drain starts with no `StoreWriter` registered
- **THEN** no worker is started and the drain does not error or exit
- **AND** rows already in the outbox remain untouched

### Requirement: Routing by persisted target store

The drain SHALL route each outbox row to the registered `StoreWriter` whose declared `StoreID` equals the row's `target_store` column. The drain SHALL NOT call the Schema Registry at delivery time. A row whose `target_store` has no registered `StoreWriter` SHALL be left pending and logged, and SHALL NOT be delivered, deleted, or cause the drain to crash.

#### Scenario: Row routed to the store that owns it

- **WHEN** a pending row has `target_store = 'telemetry'` and a `StoreWriter` with `StoreID() == 'telemetry'` is registered
- **THEN** the row is delivered through that writer
- **AND** no Schema Registry lookup is performed during delivery

#### Scenario: Unroutable target store is parked, not dropped

- **WHEN** a pending row has `target_store = 'unknown-store'` and no matching `StoreWriter` is registered
- **THEN** the row stays pending (its `batch_id` remains NULL)
- **AND** the drain continues delivering rows for the stores that are registered

#### Scenario: Empty target store value is treated as unroutable

- **WHEN** a pending row has an empty `target_store`
- **THEN** the row stays pending (its `batch_id` remains NULL)
- **AND** it is never delivered or deleted

### Requirement: Batch formation by record, byte, and linger limits

For each store the drain SHALL read pending rows in ascending `id` (outbox) order and form a batch, closing it when the store's maximum record count `N`, maximum byte size `M`, or maximum linger time `T` is first reached. `M` MAY be disabled (value 0), in which case only `N` and `T` trigger a flush. Batch membership SHALL follow outbox order with no gaps and no reordering.

#### Scenario: Flush on record count

- **WHEN** a store's limit is `N = 100` and at least 100 rows are pending
- **THEN** the first 100 rows in `id` order form one batch and are delivered
- **AND** row 101 onward is left for the next batch

#### Scenario: Flush on linger time with a single row

- **WHEN** exactly one row is pending for a store, `N` is not reached, and linger time `T` elapses
- **THEN** that single row is delivered as a batch of one
- **AND** the batch is not held back waiting for more rows

#### Scenario: A single row larger than the byte limit still ships

- **WHEN** `M` is set and one pending row's payload alone exceeds `M`
- **THEN** that row is delivered as a batch of exactly one row
- **AND** the worker does not stall or skip the row

#### Scenario: Byte limit disabled

- **WHEN** a store reports `M = 0`
- **THEN** batches flush only on `N` or `T`
- **AND** no byte-size trigger is applied

#### Scenario: Linger timer fires with no pending rows

- **WHEN** the linger timer `T` elapses while no rows are pending for the store
- **THEN** no batch is formed and `StoreWriter.Write` is not called with an empty batch

#### Scenario: Large pending set is drained in order in bounded memory

- **WHEN** more than 10,000 rows are pending for one store
- **THEN** the worker forms successive batches in ascending `id` order using a bounded scan
- **AND** it does not load all pending rows into memory at once

### Requirement: Immutable batch pinning with a monotonic identifier

Before calling `StoreWriter.Write`, the drain SHALL pin a batch by stamping its exact rows with a `batch_id` drawn from a dedicated monotonic Postgres sequence, in one atomic update over an explicit set of row `id`s. Once pinned, a batch's membership SHALL be immutable: no row is added and none removed. A concurrent insert by the ingest half (which receives a higher `id`) SHALL NOT join an already-pinned batch. A crash during pinning SHALL leave membership all-or-nothing.

#### Scenario: Pinning assigns one batch id to the exact decided rows

- **WHEN** a worker decides a batch of rows `[id=5, id=6, id=7]`
- **THEN** exactly those three rows are updated with the same new `batch_id` in one statement
- **AND** that `batch_id` is greater than every previously assigned `batch_id` for the drain

#### Scenario: Concurrent ingest insert does not join a pinned batch

- **WHEN** the ingest half inserts a new pending row while a worker is pinning a batch it already decided
- **THEN** the new row (higher `id`) is excluded from the pinned batch
- **AND** it remains pending for a later batch

#### Scenario: Crash mid-pin leaves membership all-or-nothing

- **WHEN** the drain process crashes while the pin update is in flight
- **THEN** on restart either all decided rows carry the `batch_id` or none do
- **AND** no row is left half-pinned

#### Scenario: Pin fails when the database is unavailable

- **WHEN** the pin update cannot reach the database
- **THEN** no rows are pinned and `StoreWriter.Write` is not called
- **AND** the worker backs off and retries the scan later

### Requirement: At-least-once delivery removes rows only after acknowledgement

The drain SHALL remove a batch's rows from the outbox only after `StoreWriter.Write` returns a nil error (the store acknowledged the batch). The acknowledgement and the row removal SHALL NOT need to be atomic. Deletion SHALL be scoped by `batch_id` so it removes only that batch's rows. A `nil` return SHALL mean the whole batch was accepted; there is no per-row partial result.

#### Scenario: Rows removed after the store acknowledges

- **WHEN** `StoreWriter.Write` returns nil for a pinned batch
- **THEN** exactly the rows carrying that `batch_id` are deleted from the outbox
- **AND** rows of other batches are untouched

#### Scenario: Crash after acknowledgement before deletion resends identically

- **WHEN** the store acknowledges a batch but the drain crashes before deleting the rows
- **THEN** on restart the identical pinned batch is resent under the same `batch_id`
- **AND** after the store deduplicates it, the rows are deleted

#### Scenario: Delete failure leaves the batch pinned for retry

- **WHEN** the store acknowledges a batch but the delete fails because the database is unavailable
- **THEN** the batch's rows stay pinned with their `batch_id`
- **AND** the batch is resent under the same `batch_id` on the next attempt and deduplicated by the store

#### Scenario: Concurrent deletes of different batches do not interfere

- **WHEN** two workers delete their own acknowledged batches at the same time
- **THEN** each delete removes only rows matching its own `batch_id`
- **AND** neither delete removes the other batch's rows

### Requirement: Per-store back-pressure on overload and unexpected errors

When `StoreWriter.Write` returns the store-overload sentinel, the drain SHALL pause that store and retry the same pinned batch with exponential back-off plus jitter, capped at a configured maximum interval. Any error that is neither the overload sentinel nor a permanent-failure error SHALL be treated the same as overload (pause and retry the same batch). Retries SHALL be unbounded by default. A configured maximum retry count greater than zero SHALL bound the retries for a single batch; when that count is exhausted the drain SHALL log the batch (its `batch_id` and last error) and discard it by deleting its rows, then continue with the next batch. Back-pressure on one store SHALL NOT pause any other store.

#### Scenario: Overloaded store pauses and retries the same batch

- **WHEN** `StoreWriter.Write` returns the overload sentinel for a pinned batch
- **THEN** the worker waits a backed-off, jittered interval and retries the identical pinned batch
- **AND** it does not form a new batch or advance until the batch is accepted

#### Scenario: Back-off is bounded by the configured maximum

- **WHEN** a store stays overloaded across many consecutive retries
- **THEN** the retry interval grows exponentially but never exceeds the configured maximum interval

#### Scenario: Retries are unbounded by default

- **WHEN** the max-retry count is zero (the default) and a store stays overloaded
- **THEN** the worker keeps retrying the same pinned batch indefinitely with capped back-off
- **AND** it never discards the batch

#### Scenario: Exhausting the max-retry count discards the batch

- **WHEN** the max-retry count is a positive N and a batch fails with overload or an unexpected error N times
- **THEN** the drain logs the batch's `batch_id` and last error and deletes its rows from the outbox
- **AND** the worker proceeds to the next batch

#### Scenario: Other stores keep draining during a pause

- **WHEN** the `telemetry` store is paused in overload back-off
- **THEN** the `inventory` worker continues to form, deliver, and delete batches

#### Scenario: Unexpected error is treated like overload

- **WHEN** `StoreWriter.Write` returns an error that is neither the overload sentinel nor a permanent-failure error
- **THEN** the worker pauses that store and retries the same pinned batch with back-off
- **AND** it does not delete the batch's rows (unless the max-retry count is exhausted)

#### Scenario: Context cancellation during Write stops cleanly

- **WHEN** the drain is shutting down (context cancelled) while a `Write` is in flight
- **THEN** the worker stops after the call returns without deleting the batch
- **AND** the pinned batch is left intact for the next run

### Requirement: Permanent failure drops the batch and keeps the store live

When `StoreWriter.Write` returns a permanent-failure error, the drain SHALL log the error and delete the batch's rows from the outbox. This is a documented permanent loss of that batch's data. The store SHALL continue to accept new batches afterward.

#### Scenario: Permanent failure removes the batch and logs it

- **WHEN** `StoreWriter.Write` returns a permanent-failure error for a pinned batch
- **THEN** the batch's rows are deleted from the outbox and the error is logged
- **AND** the data in that batch is not retried

#### Scenario: Store keeps draining after a permanent failure

- **WHEN** a batch fails permanently and subsequent pending rows exist for the same store
- **THEN** the worker forms and delivers the next batch normally

### Requirement: Restart durability replays pinned batches identically

Outbox contents and delivery progress SHALL survive a restart of Data Ingestion. On startup, before scanning new pending rows, each worker SHALL resend any rows already pinned with a `batch_id` for its store, grouped by `batch_id`, in ascending `id` order within each batch, under the same `batch_id`. No row SHALL be added to or removed from a pinned batch across the restart.

#### Scenario: Pinned batch is resent before new rows after restart

- **WHEN** the drain restarts and finds rows already stamped with a `batch_id` for a store
- **THEN** that store's worker resends exactly those rows under the same `batch_id` before scanning unpinned pending rows
- **AND** no pending (NULL `batch_id`) row is merged into the resent batch

#### Scenario: A pinned batch is never re-pinned under a new id

- **WHEN** a worker resumes a batch that already has a `batch_id`
- **THEN** it reuses that `batch_id` and does not assign a new one

#### Scenario: Multiple pinned batches resume in batch-id order

- **WHEN** two distinct pinned `batch_id`s exist for one store at startup
- **THEN** the worker resends them in ascending `batch_id` order, each as its original immutable membership

### Requirement: StoreWriter contract and limit validation at registration

The drain SHALL depend only on a shared `StoreWriter` contract: it declares the `StoreID` it serves, reports its `BatchLimits` (`N` records, `M` bytes, `T` linger), and writes one immutable batch, signalling acceptance (nil), overload (overload sentinel), or permanent failure (permanent-failure error). The store-overload and permanent-failure sentinels SHALL be distinct from the existing `ingest.ErrOverloaded` (pipeline overload). At registration the drain SHALL reject invalid limits: `N <= 0`, `T <= 0`, negative `M`, or a duplicate `StoreID`.

#### Scenario: Valid StoreWriter registers and drives a worker

- **WHEN** a `StoreWriter` declares a unique `StoreID` and limits `N > 0`, `T > 0`, `M >= 0`
- **THEN** registration succeeds and a worker is started for its `StoreID`

#### Scenario: Zero record limit is rejected

- **WHEN** a `StoreWriter` reports `N = 0`
- **THEN** registration returns an error and no worker is started for it

#### Scenario: Zero linger limit is rejected

- **WHEN** a `StoreWriter` reports `T = 0`
- **THEN** registration returns an error and no worker is started for it

#### Scenario: Duplicate StoreID is rejected

- **WHEN** two `StoreWriter`s declare the same `StoreID`
- **THEN** registration returns an error for the second
- **AND** only one worker exists for that `StoreID`

#### Scenario: Store-overload sentinel is distinct from pipeline overload

- **WHEN** the drain inspects a `Write` error
- **THEN** it matches the delivery-half overload sentinel, not `ingest.ErrOverloaded`
- **AND** errors are matched with `errors.Is` / `errors.As`, never by message text

### Requirement: Drain configuration validation

The drain SHALL load its poll interval, back-off settings (initial interval, maximum interval, jitter), and a maximum retry count from the existing layered `OCG_DI_*` configuration and SHALL reject invalid values at startup. The poll interval SHALL be greater than zero. The maximum back-off interval SHALL be greater than or equal to the initial interval and greater than zero. The maximum retry count SHALL be greater than or equal to zero, where zero means unbounded retries.

#### Scenario: Valid configuration starts the drain

- **WHEN** the poll interval is positive, the back-off maximum is greater than or equal to a positive initial interval, and the max-retry count is zero or positive
- **THEN** configuration validation passes and the drain starts

#### Scenario: Zero poll interval fails startup

- **WHEN** the configured poll interval is zero
- **THEN** configuration validation returns an error and the drain does not start

#### Scenario: Back-off maximum below initial fails startup

- **WHEN** the configured maximum back-off interval is smaller than the initial interval
- **THEN** configuration validation returns an error and the drain does not start

#### Scenario: Negative max-retry count fails startup

- **WHEN** the configured maximum retry count is negative
- **THEN** configuration validation returns an error and the drain does not start
