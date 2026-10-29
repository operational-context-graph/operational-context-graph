<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
SPDX-License-Identifier: Apache-2.0
-->

# Inventory-store: DAO → Repository migration sketch

Status: **design sketch + Asset-only vertical slice**. Not a cutover. Relationship and all
writes are untouched.

## Why

Today `internal/store` is a DAO. One struct (`store.Asset`) does double duty as both the
SQL row and the value returned to callers. The temporal invariants (one open version per
natural key, contiguous `valid_from`/`valid_to`) live only in SQL and in the upsert
functions. There is no domain type, so the rules cannot be unit-tested without Postgres,
and persistence vocabulary (`Cursor`, `BatchResult`) leaks through the consumer interface.

This migration introduces a domain model separated from the row model, accessed through a
repository. We do **not** re-implement the temporal logic in Go — the SQL partial unique
index and transactional expire already enforce it. We only separate the models and add a
factory that guards reconstitution.

## Target shape

Package names follow the ports-and-adapters convention Wild Workouts uses: domain in its
own package, all repository implementations together in an `adapters` package, each
implementation named `<Backend><Entity>Repository` (e.g. `PostgresAssetRepository`) with a
lowercase backend-prefixed row DTO (`postgresAsset`, mirroring WW's `mysqlHour`).

```
internal/inventory/          # domain, DB-agnostic
    asset.go                 #   Asset aggregate (unexported fields), Validity, NaturalKey
    reconstitute.go          #   ReconstituteAsset(...) factory — the only way to rebuild from storage

internal/inventory/adapters/ # repository implementations (all backends live here, package `adapters`)
    asset_postgres_repository.go  # PostgresAssetRepository: SQL identical to today's store
    asset_postgres_row.go         # postgresAsset row DTO + toDomain / toRow mapping

internal/httpapi/            # consumer — defines the interface it needs (Go guideline: Camp A)
    reader.go                #   assetReader interface + dao/repo adapters + compare wrapper
```

One deliberate divergence from Wild Workouts: WW puts the `Repository` interface in the
domain package (`domain/hour/repository.go`). We keep it consumer-side in `httpapi`, per the
official Go guideline chosen earlier ("interfaces belong in the consuming package").

`internal/store` stays exactly as-is. Nothing is deleted in this slice.

## The three layers, concretely

1. **Domain `inventory.Asset`** — unexported fields; only constructed via the factory, so an
   invalid temporal state (e.g. `validTo` before `validFrom`) cannot exist in memory.
2. **Row `adapters.postgresAsset`** — mirrors the table, `db`-shaped, unexported. This is
   today's `store.Asset`, demoted to a pure DTO (the role WW's `mysqlHour` plays).
3. **Mapping** — `toDomain` (row → domain, invariant-guarded) and `toRow` (domain → row).

## Feature flag: three states, reads only

`OCG_IS_READ_PATH` (config key `read_path`), default `dao`:

| Value     | Who serves the response | Comparison |
| --------- | ----------------------- | ---------- |
| `dao`     | existing `store`        | none       |
| `repo`    | new `adapters` repo     | none       |
| `compare` | existing `store`        | both run; results diffed; mismatches logged with asset ID + differing field |

- **Reads only.** Only `GetAsset` and `ListAssets` route through the flag. `compare` is safe
  because reads have no side effects and both paths hit the same rows, so equal input must
  produce equal output.
- Writes (`UpsertAsset`, batch) and all of Relationship always use the existing `store`.

## Migration path (after this slice is reviewed)

1. Run `compare` in staging. Confirm zero mismatches across representative traffic.
2. Flip to `repo` in staging, then production. Old path dormant.
3. Extend the same pattern to writes and to Relationship.
4. Delete the DAO read methods once `repo` is authoritative everywhere.

This is the strangler pattern: new code grows beside old, proven equivalent, then old dies.
