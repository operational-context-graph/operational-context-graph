# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Build stage.
# Build context must be the repository root so go.work resolves gen/go.
# Example: docker build -f services/data-ingestion/Dockerfile .
FROM golang:1.27 AS build
WORKDIR /src

COPY go.work go.work.sum ./
COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/data-ingestion/go.mod services/data-ingestion/go.sum services/data-ingestion/
RUN go mod download -x 2>/dev/null; true

COPY gen/go/ gen/go/
COPY services/data-ingestion/ services/data-ingestion/
RUN CGO_ENABLED=0 go build -trimpath -o /out/data-ingestion \
    github.com/operational-context-graph/operational-context-graph/services/data-ingestion/cmd/data-ingestion

# Runtime stage.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/data-ingestion /usr/local/bin/data-ingestion
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/data-ingestion"]
