<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

# Config Specification

## Purpose

Config validation for the `IngestionService` scope: gRPC server host and port and TLS, request size and count limits, and retry hint parameters. Zero-value or invalid configs fail fast at `config.Load` before the service starts.

## Requirements

### Requirement: GRPCConfig required fields fail fast at load

`config.Load` SHALL return a non-nil error when `GRPCConfig.Port` is missing or out of range (1–65535). `GRPCConfig.Host` SHALL default to `"0.0.0.0"`. `GRPCConfig.Port` SHALL default to `9090`.

#### Scenario: Missing gRPC port uses default
- **WHEN** `GRPCConfig.Port` is not set
- **THEN** the resolved value is `9090`

#### Scenario: Host defaults to 0.0.0.0
- **WHEN** `GRPCConfig.Host` is not set
- **THEN** the resolved value is `"0.0.0.0"`

#### Scenario: Port out of range fails
- **WHEN** `GRPCConfig.Port` is set to a value outside 1–65535
- **THEN** `Load` returns a non-nil error; the process does not start

#### Scenario: Valid host and port accepted
- **WHEN** `GRPCConfig.Host` and `GRPCConfig.Port` are valid
- **THEN** `Load` succeeds (assuming other required fields are also valid)

### Requirement: Environment variable hierarchy separator is double underscore

Environment variables MUST use the prefix `OCG_DI_`. The double-underscore `__` SHALL separate hierarchy levels; a single underscore is preserved as part of a field name.

#### Scenario: Double underscore maps to hierarchy separator
- **WHEN** `OCG_DI_GRPC__HOST=127.0.0.1` is set
- **THEN** the resolved `GRPCConfig.Host` is `"127.0.0.1"`

#### Scenario: Double underscore separates hierarchy; single underscore is part of the field name
- **WHEN** `OCG_DI_GRPC__TLS__CERT_FILE=/etc/tls/tls.crt` is set
- **THEN** the resolved `GRPCConfig.TLS.CertFile` is `"/etc/tls/tls.crt"`

### Requirement: Partial TLS configuration is an error

If any one of `GRPCConfig.TLSCertFile`, `TLSKeyFile`, `TLSCAFile` is set, all three SHALL be set. Setting none is valid (plaintext mode). Setting one or two is an error.

#### Scenario: Cert-only → error
- **WHEN** only `TLSCertFile` is set
- **THEN** `Load` returns a non-nil error naming the missing fields

#### Scenario: All three TLS paths set → accepted
- **WHEN** all three TLS path fields are non-empty
- **THEN** `Load` returns a valid config

#### Scenario: No TLS paths → plaintext accepted
- **WHEN** all three TLS path fields are empty
- **THEN** `Load` returns a valid config

### Requirement: LimitsConfig.MaxMessages defaults and validates

`MaxMessages` SHALL default to 1000 when unset (0). A negative value SHALL cause `Load` to return an error. A positive value SHALL be used as-is.

#### Scenario: Zero defaults to 1000
- **WHEN** `MaxMessages` is 0
- **THEN** the resolved value is 1000

#### Scenario: Negative value is an error
- **WHEN** `MaxMessages` is -1
- **THEN** `Load` returns a non-nil error

#### Scenario: Explicit positive value preserved
- **WHEN** `MaxMessages` is 500
- **THEN** the resolved value is 500

### Requirement: LimitsConfig.MaxPayloadBytes defaults and validates

`MaxPayloadBytes` SHALL default to 4,194,304 bytes (4 MiB) when unset (0). A negative value SHALL cause `Load` to return an error.

#### Scenario: Zero defaults to 4 MiB
- **WHEN** `MaxPayloadBytes` is 0
- **THEN** the resolved value is 4,194,304

#### Scenario: Negative value is an error
- **WHEN** `MaxPayloadBytes` is -1
- **THEN** `Load` returns a non-nil error

#### Scenario: Explicit positive value preserved
- **WHEN** `MaxPayloadBytes` is 2,097,152
- **THEN** the resolved value is 2,097,152

### Requirement: RetryConfig.RetryDelay defaults and validates

`RetryDelay` SHALL default to 1 second when unset (zero duration). A negative or
zero duration explicitly set SHALL cause `config.Load` to return a non-nil error.
A positive value SHALL be used as-is and placed into the `RetryInfo.retry_delay`
field of `codes.Unavailable` gRPC responses. The env var is `OCG_DI_RETRY__RETRY_DELAY`.

#### Scenario: Zero defaults to 1 second
- **WHEN** `RetryDelay` is the zero duration (not set)
- **THEN** the resolved value is 1 second

#### Scenario: Negative duration is an error
- **WHEN** `RetryDelay` is a negative duration
- **THEN** `Load` returns a non-nil error; the process does not start

#### Scenario: Explicit positive duration preserved
- **WHEN** `RetryDelay` is 5 seconds
- **THEN** the resolved value is 5 seconds

#### Scenario: RetryDelay is reflected in UNAVAILABLE response hint
- **WHEN** the pipeline returns `ErrOverloaded` or `ErrUnavailable` and `RetryDelay` is 3 seconds
- **THEN** the gRPC response carries `RetryInfo.retry_delay` equal to 3 seconds

#### Scenario: Former env var OCG_DI_RETRY__INITIAL_BACKOFF is no longer recognised
- **WHEN** `OCG_DI_RETRY__INITIAL_BACKOFF` is set and `OCG_DI_RETRY__RETRY_DELAY` is not set
- **THEN** `RetryDelay` resolves to the default (1 second); the old env var is silently ignored

### Requirement: GRPCConfig.ShutdownTimeout defaults and validates

`ShutdownTimeout` SHALL default to 30 seconds when unset. A negative or zero value SHALL cause `Load` to return an error.

#### Scenario: Zero defaults to 30 seconds
- **WHEN** `ShutdownTimeout` is 0
- **THEN** the resolved value is 30 seconds

#### Scenario: Negative value is an error
- **WHEN** `ShutdownTimeout` is a negative duration
- **THEN** `Load` returns a non-nil error

### Requirement: Config layered from defaults, file, and environment

`config.Load` SHALL apply: built-in defaults → optional YAML file (path from `OCG_DI_CONFIG_FILE`) → `OCG_DI_*` environment variables, in that order. Later sources override earlier ones.

#### Scenario: Environment overrides file
- **WHEN** the YAML file sets `grpc.host` to `0.0.0.0` and `OCG_DI_GRPC__HOST=127.0.0.1` is set
- **THEN** the resolved `GRPCConfig.Host` is `"127.0.0.1"`

## Non-goals

- Runtime config reloading.
- Per-connection or per-client config overrides.
