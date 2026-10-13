<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

# gRPC Server Specification

## Purpose

The gRPC server lifecycle component starts `IngestionService` on a configurable host and port, enforces mutual TLS when configured, aligns the gRPC receive buffer with `LimitsConfig`, and shuts down gracefully when the parent context is cancelled.

## Requirements

### Requirement: Server starts on configured host and port

`Server.Run(ctx)` SHALL bind the gRPC listener on `GRPCConfig.Host:GRPCConfig.Port` and begin serving. It SHALL return a non-nil error and not start when the port is already in use.

#### Scenario: Successful start
- **WHEN** `GRPCConfig.Host` and `GRPCConfig.Port` are valid and the port is free
- **THEN** `Server.Run` starts serving and blocks until `ctx` is cancelled

#### Scenario: Port already in use
- **WHEN** `GRPCConfig.Port` names a port already bound by another process
- **THEN** `Server.Run` returns a non-nil error immediately without blocking

#### Scenario: Already-cancelled context
- **WHEN** `Run` is called with an already-cancelled context
- **THEN** `Run` returns without starting the listener or accepting any connections

### Requirement: mTLS enforced when all TLS paths are configured

When `GRPCConfig.TLSCertFile`, `TLSKeyFile`, and `TLSCAFile` are all set, the server SHALL require mutual TLS (TLS 1.3 minimum, `RequireAndVerifyClientCert`). A client presenting no certificate or a certificate from an untrusted CA SHALL be rejected before any RPC handler is invoked.

#### Scenario: Valid mTLS client accepted
- **WHEN** a client connects with a certificate signed by the configured CA
- **THEN** the TLS handshake succeeds and the `Ingest` RPC is dispatched

#### Scenario: Client without certificate rejected in mTLS mode
- **WHEN** a client connects without a client certificate while mTLS is configured
- **THEN** the TLS handshake fails; no RPC handler is invoked

#### Scenario: Certificate from wrong CA rejected
- **WHEN** a client presents a certificate signed by a CA not in the configured pool
- **THEN** the TLS handshake fails; no RPC handler is invoked

#### Scenario: Expired client certificate rejected
- **WHEN** a client presents a certificate whose `NotAfter` is in the past
- **THEN** the TLS handshake fails; no RPC handler is invoked

#### Scenario: Invalid CA file causes Run to fail
- **WHEN** `TLSCAFile` exists on disk but does not contain valid PEM data
- **THEN** `Server.Run` returns a non-nil error and does not start the listener

#### Scenario: Missing cert file causes Run to fail
- **WHEN** `TLSCertFile` path does not exist on disk
- **THEN** `Server.Run` returns a non-nil error and does not start the listener

### Requirement: Plaintext mode when no TLS paths configured

When none of `TLSCertFile`, `TLSKeyFile`, `TLSCAFile` are set, the server SHALL start in plaintext gRPC mode.

#### Scenario: Plaintext mode starts successfully
- **WHEN** all three TLS path fields are empty
- **THEN** `Server.Run` starts in plaintext mode

### Requirement: MaxRecvMsgSize set to MaxPayloadBytes

The gRPC server SHALL set `MaxRecvMsgSize` to exactly `LimitsConfig.MaxPayloadBytes`. Proto framing overhead (field tags, length prefixes, string fields) counts against this limit. Requests where framing pushes the wire size over `MaxPayloadBytes` are rejected by the transport with `RESOURCE_EXHAUSTED`. This is the sole payload size guard; no application-layer byte-counting loop is used.

#### Scenario: Small payload accepted
- **WHEN** a client sends a request whose total wire size is below `MaxPayloadBytes`
- **THEN** the gRPC transport accepts it and the handler receives the request

#### Scenario: Oversized wire message rejected by transport
- **WHEN** a client sends a request whose total wire size exceeds `MaxPayloadBytes`
- **THEN** the gRPC transport rejects it with `RESOURCE_EXHAUSTED` before the handler is invoked

### Requirement: Startup logs registered services

After binding the listener and registering handlers, `Server.Run` SHALL log the names of all registered gRPC services.

#### Scenario: Registered services logged on start
- **WHEN** `Server.Run` starts successfully
- **THEN** a log entry lists all registered service names (e.g. `ocg.ingestion.v1.IngestionService`)

### Requirement: Graceful shutdown on context cancellation

When the parent context is cancelled, `Server.Run` SHALL call `GracefulStop()` to drain in-flight RPCs before returning. After `GracefulStop` returns, `Run` SHALL return nil.

#### Scenario: In-flight RPC completes during shutdown
- **WHEN** `ctx` is cancelled while one `Ingest` RPC is executing
- **THEN** `GracefulStop` waits for the RPC to complete before `Run` returns; the RPC response is delivered to the client

#### Scenario: Clean shutdown with no in-flight RPCs
- **WHEN** `ctx` is cancelled with no active RPCs
- **THEN** `Run` returns nil promptly

#### Scenario: Shutdown timeout hard-stops the server
- **WHEN** an in-flight RPC does not complete within `GRPCConfig.ShutdownTimeout`
- **THEN** the server calls `Stop()` (forceful) and `Run` returns; in-flight RPCs receive an error

### Requirement: Run is not re-entrant

`Server.Run` SHALL return an error immediately if called while another call to `Run` is already active on the same `Server` instance.

#### Scenario: Second concurrent Run call fails
- **WHEN** two goroutines call `Run` on the same `Server` simultaneously
- **THEN** the second call returns a non-nil error without binding a second listener

## Non-goals

- `BackfillService` or OTLP service registration — separate tickets.
- gRPC reflection, rate limiting.
- Connector identity extraction from mTLS CN — separate interceptor ticket.
- Certificate rotation / hot reload.
- Per-RPC compression (GZIP) — can be added via a server option later.
