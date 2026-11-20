> [!CAUTION] This is generated documentation that has not been reviewed, yet. Parts of it will be integrated
> into the existing component documentation, this file needs to be deleted before merging the feature branch.

# Schema Introspection API Operations

## Configuration

The service loads defaults, optional YAML, and `OCG_SCHEMA_INTROSPECTION_` environment overrides, in that order.
Set `OCG_SCHEMA_INTROSPECTION_CONFIG_FILE` to select a YAML file.
Double underscores separate nested configuration keys.

Supply the database URL through `OCG_SCHEMA_INTROSPECTION_DATABASE__URL` or secret-mounted YAML at `database.url`.
The URL MUST identify an absolute PostgreSQL target with a login name.
Users MUST keep credentials in secret-backed configuration, not source files, command examples, or logs.
Treat database URLs as trusted operator configuration, not API request input.
Driver certificate parameters can reference files accessible to the service process.

Set a positive `admission.max_in_flight` value before startup.
The pool MUST contain at least two connections and reserve at least one connection for readiness.
Admission MUST NOT exceed unreserved pool capacity.
Timeouts MUST remain positive. Retry intervals MUST NOT be negative.
The [example YAML](../configs/schema-introspection-api.example.yaml) contains no database credentials.

## User-controlled database SSL

Users choose Secure Sockets Layer (SSL) configuration through the corresponding database connection URL parameters.
The service passes those parameters to pgx without adding transport-security policy checks or overrides.
This behavior applies to startup, readiness, request reads, concurrent connections, and replacement connections.
The `database.production` setting does not change this behavior.

Transport Layer Security (TLS) policy validation and enforcement for every database connection are explicit service-level non-goals.
The service MUST NOT require encryption, peer verification, or a specific SSL mode.
It MUST NOT impose a minimum TLS version.
It MUST NOT reject URL parameters because of their security properties.

Users MAY select any driver-supported SSL mode:

| URL parameter | Driver-selected behavior |
| --- | --- |
| `sslmode=disable` | Use plaintext transport. |
| `sslmode=allow` | Try plaintext before TLS. |
| `sslmode=prefer` | Try TLS before plaintext fallback. |
| `sslmode=require` | Require TLS; certificate verification depends on other driver options. |
| `sslmode=verify-ca` | Verify the certificate chain without requiring hostname verification. |
| `sslmode=verify-full` | Verify the certificate chain and hostname. |

Omitted SSL parameters retain pgx defaults, including its default `prefer` behavior.
Configured fallback behavior remains unchanged.
Options such as `sslrootcert=system` can change effective verification according to pgx rules.
The service does not override those rules.

Certificate paths, client certificates, key paths, negotiation settings, and other SSL parameters remain driver-controlled.
For example, users can supply `sslrootcert`, `sslcert`, `sslkey`, `sslcertmode`, `sslnegotiation`, and `sslsni`.
Parameter support depends on the pinned driver version; not every libpq parameter has equivalent pgx support.
The service does not implement missing driver features or interpret unsupported TLS protocol-version parameters.
It does not pin TLS 1.3 or replace driver protocol defaults.

Removing service-level enforcement does not disable verification that users request.
Invalid parameters, missing certificate files, certificate failures, negotiation failures, and connection failures retain normal driver error handling.
Startup fails when its bounded database check fails.
Readiness reports unavailable storage; failed request reads retain classified dependency errors and resource cleanup.
Successful configuration loading does not guarantee the driver accepts the URL or the database accepts the connection.

Users own database transport security.
Disabled encryption can expose credentials and data to interception.
Disabled verification can allow peer spoofing even when traffic is encrypted.
Users SHOULD choose settings that meet their deployment's security requirements.

## Read-only storage and operations

Every runtime connection assumes the `schema_registry_api` role and uses a fixed safe `search_path`.
Static parameterized SQL and read-only transactions remain mandatory regardless of SSL configuration.
Use separate migration credentials outside the API process and deployment.
Goose migration targets consume their independently supplied `DATABASE_URL` without service-level TLS policy enforcement.
The API does not run migrations or write registry data at startup.

`GET` and `HEAD /healthz` report process liveness.
`GET` and `HEAD /readyz` execute a bounded PostgreSQL query using reserved pool capacity.
Readiness does not require an active release.
Shutdown marks readiness as draining, refuses new admission, cancels outstanding work, and closes the pool within its deadline.

Database administrators remain trusted actors.
Administrative auditing, encrypted backups, point-in-time recovery, retention monitoring, and restore checks protect immutable history operationally.

## API and scope boundaries

Consumers pin related reads to one schema release and follow every candidate mapping page before evaluating conditions externally.
Mappings preserve optional conditions, ordered transformations, exact program bytes, and complete package models.
The Registry does not interpret or execute programs.
Format validation and reference resolution remain offline and release-scoped.
Pagination cursors remain unsigned, opaque, and non-expiring; normal validation and visibility checks still apply.

Authentication and authorization remain deferred to a later change.
The upstream load balancer terminates inbound HTTPS/TLS; the API service does not manage inbound certificates.
All schema release activation processes and mechanisms remain outside this implementation, including test-only activation helpers.
The API reads pre-populated release state without activating, repairing, or mutating it.

## Database SSL contract tests

Run these commands from `services/schema-registry`:

```sh
make test TEST_PACKAGES='./internal/config/introspection ./internal/storage/postgres'
make test TEST_PACKAGES='./test/integration/postgres' TEST_ARGS='-run TestRuntimePoolSuite'
make vet
make lint
make build
```

Tests use race detection. Runtime pool integration tests require a Docker-compatible runtime.
Unit tests cover all SSL modes, omitted settings, and preservation of primary and fallback transport configuration.
PostgreSQL smoke tests cover plaintext, verified TLS 1.2, readiness without an active release, store reads, and shared pool closure.
Configuration loading and invalid store timeouts have unit coverage. The smoke tests do not retest driver concurrency or reconnection.
Existing HTTP tests cover populated requests; one real certificate-verification failure checks the user's requested trust policy.
Fixture TLS versions and certificate policies are test inputs, not service-level requirements.
