# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker buildx build --platform linux/amd64 --provenance=false -t ghcr.io/operational-context-graph/inventory-connector:latest -f services/connectors/inventory/Dockerfile .
FROM golang:1.27 AS build
WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/connectors/inventory/go.mod services/connectors/inventory/go.sum services/connectors/inventory/
RUN go work init gen/go services/connectors/inventory && go mod download -x 2>/dev/null; true

COPY gen/go/ gen/go/
COPY services/connectors/inventory/ services/connectors/inventory/
RUN CGO_ENABLED=0 go build -trimpath -o /out/inventory-connector \
    github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/cmd/connector

# Runtime stage.
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/inventory-connector /usr/local/bin/inventory-connector
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/inventory-connector"]
