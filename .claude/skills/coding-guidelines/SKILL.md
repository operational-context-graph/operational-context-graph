---
name: coding-guidelines
description: >
  General coding guidelines for the project. This skill provides style guides, common practices and
  patterns, and a pre-defined set of to be used libraries and frameworks for all programming languages
  used in the project.
---

# Coding Guidelines

General coding guidelines for all programming languages used in the project.

## Go Development

All Go code MUST follow the [Google Go Style Guide](https://google.github.io/styleguide/go/guide).

All Go code MUST follow the [Google Go Style Best Practices](https://google.github.io/styleguide/go/best-practices) on
how to best apply the Go Style Guide.

### Libraries and Tools

All components and services in this project MUST use the following libraries, frameworks, and tools for the defined
purpose:

- The [Gin Web Framework](https://gin-gonic.com/en/docs/) (Go package `github.com/gin-gonic/gin`) for all  HTTP-based
  APIs, especially for all RESTful APIs.
- [koanf](https://github.com/knadh/koanf) (Go package `github.com/knadh/koanf/v2`) for reading configuration files and
  [go-playground/validator](https://github.com/go-playground/validator) (Go package `github.com/go-playground/validator/v10`)
  to perform content validation on the loaded configuration. All services in the project should use the combination of
  these two libraries to define and load application configuration from static configuration files and support overriding
  configuration values through environment variables. Each service must additionally define dependencies for the `koanf`
  providers it intends to use / support (e.g. `github.com/knadh/koanf/providers/file`, `github.com/knadh/koanf/providers/env/v2`),
  etc. The preferred file format for static configuration files is YAML, and the `koanf` YAML parser from the Go package
  `github.com/knadh/koanf/parsers/yaml` should be used to parse YAML configuration files.
- All tests should make use of the assertions and mock functionality provided by the [testify](https://github.com/stretchr/testify)
  testing toolkit (Go package `github.com/stretchr/testify`). The [mockery](https://github.com/vektra/mockery) mock code
  autogenerator (Go package `github.com/vektra/mockery/v3`) can be optionally used per component as a code generator for
  `testify/mock`-compatible mocks. It can be used in components where interfaces are large or change frequently, but
  smaller components that hand-write their mocks do not need to adopt it. Adoption of mock generation MUST be an explicit
  design decision defined by the developer, don't adopt this technique per se in any of the components.
- The [Goose](https://github.com/pressly/goose) database migration tool for evolving database schemas.

One of the following database drivers MUST be used for the respective database technologies:

- [pgx](https://github.com/jackc/pgx) (Go package `github.com/jackc/pgx/v5`) as the driver and toolkit for PostgreSQL.
- [go-mysql-org/go-mysql](https://github.com/go-mysql-org/go-mysql) (Go package `github.com/go-mysql-org/go-mysql/driver`)
  for all databases supporting the MySQL network protocol (e.g. Apache Doris). The library also provides a MySQL client
  package `github.com/go-mysql-org/go-mysql/client` which supports connection pooling and response streaming. Business
  logic requiring such functionality should make use of this client package.

## Helm Charts

### Tests

Use the Helm [unittest](https://github.com/helm-unittest/helm-unittest) plugin to define unit tests for each Helm chart.

All tests should be created in a dedicated `tests` directory in the chart. Individual test files should follow the
defined templates, i.e., each template file should be complemented with a test file following the naming schema below.

```text
<chart-directory>
├── Chart.yaml
├── README.md
├── templates
│   ├── some-deployment.yaml
│   ├── some-service.yaml
│   └── ...
├── tests
│   ├── some-deployment_test.yaml
│   ├── some-service_test.yaml
│   └── ...
└── values.yaml
```
