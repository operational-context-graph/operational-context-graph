<!-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Schema Registry Tests

This directory contains test assets that cross package boundaries.

## Directory structure

- `fixtures/` contains repository-owned test data, organized by bounded context.
- `integration/` contains black-box service boundary tests.
- `benchmark/` contains black-box benchmarks that use supported package APIs.

Unit tests remain beside their implementation. They may exercise unexported identifiers. Integration tests
and benchmarks use supported package APIs. This keeps those  suites runnable from this directory.
