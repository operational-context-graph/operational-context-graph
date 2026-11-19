# Schema Registry Tests

This directory contains test assets that cross package boundaries.

## Directory structure

- `fixtures/` contains repository-owned test data, organized by bounded context.
- `shared/` contains shared test helper packages.
- `integration/` contains black-box service boundary tests.
- `benchmark/` contains black-box benchmarks that use supported package APIs.
- `fuzz/` contains black-box fuzz targets that use supported package APIs.

Unit tests remain beside their implementation. They may leverage unexported identifiers. Integration tests,
benchmarks, and fuzz targets use exported package APIs. This keeps these suites runnable from this directory.
