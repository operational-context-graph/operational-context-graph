# Schema Registry

## Local Development

### Prerequisites

The following tools need to be installed to build and run the service locally:

- [Go](https://go.dev/) 1.27
- [golangci-lint](https://golangci-lint.run/docs/welcome/install/local/) for linting all source files
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for reporting known vulnerabilities that
  affect Go code
- [Syft](https://github.com/anchore/syft) for generating a Software Bill of Materials (SBOM)
- [Grant](https://github.com/anchore/grant) for license compliance evaluation of all dependencies
- [Goose](https://github.com/pressly/goose) CLI for running database migrations
- [Docker](https://www.docker.com/) (or a Docker-API compatible container runtime) for building container images
  and running [Testcontainers](https://golang.testcontainers.org/system_requirements/docker/) in the test suite

### Building the application

To build the application, run:

```sh
make build
```

### Linting and Static Analysis

To lint all source files, run:

```sh
make lint
```

To inspect Go source code for suspicious constructs, run:

```sh
make vet
```

To check what known vulnerabilities in the dependencies are reachable in the application's source code, run:

```sh
make vuln
```

### Testing

To run the full test suite (with race detection), including unit tests and integration tests, run:

```sh
make test
```

To only run the tests for individual (test) packages, run:

```sh
make test TEST_PACKAGES='./internal/introspection/release ./internal/introspection/httpapi'
```

Additional `Makefile` targets are available to run individual tests suites:

```sh
make test-unit
make test-integration
make test-benchmark
```

The integration tests make use of [Testcontainers](https://golang.testcontainers.org/) to create container-based
dependencies for the application (e.g. the PostgreSQL database). No additional setup is required here, the
Testcontainers setup is fully integrated into the test suite itself.

All test-related `Makefile` targets support supplying additional [arguments](https://pkg.go.dev/cmd/go/internal/test)
to the underlying `go test` command:

```shell
make test-benchmark TEST_ARGS='-benchtime=1x'
```

### Open Source license compliance check

To check if all dependencies of the application meet the license requirements of the project, create a software bill of
materials (SBOM) and run the license check on the created SBOM:

```sh
make sbom
make oss-license-check
```
