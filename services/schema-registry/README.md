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
golangci-lint run
```

To examines Go source code for suspicious constructs, run:

```sh
go vet ./...
```

To check what known vulnerabilities in the dependencies are reachable in the application's source code, run:

```sh
make vuln
```

### Testing

To run the full test suite (with race detection), including unit tests and integration tests, run:

```sh
go test ./... -race
```

A convenience `Makefile` target is available to run the test suite:

```shell
make test
```

The integration tests make use of [Testcontainers](https://golang.testcontainers.org/) to create container-based
dependencies for the application (e.g. the PostgreSQL database). No additional setup is required here, the
Testcontainers setup is fully integrated into the test suite itself.

### Open Source license compliance check

To check if all dependencies of the application meet the license requirements of the project, create a software bill of
materials (SBOM) and run the license check on the created SBOM:

```sh
make sbom
make oss-license-check
```
