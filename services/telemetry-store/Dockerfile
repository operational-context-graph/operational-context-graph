# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker build -f services/telemetry-store/Dockerfile .
FROM golang:1.27 AS build
WORKDIR /src

COPY services/telemetry-store/go.mod services/telemetry-store/go.sum services/telemetry-store/
RUN cd services/telemetry-store && go mod download

COPY services/telemetry-store/ services/telemetry-store/
RUN CGO_ENABLED=0 go build -trimpath -o /out/telemetry-store \
    github.com/operational-context-graph/operational-context-graph/services/telemetry-store/cmd/telemetry-store

# Goose stage — build the migration CLI independently so it does not affect go.mod.
FROM golang:1.27 AS goose
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.24.0

# Runtime stage.
FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/telemetry-store /usr/local/bin/telemetry-store
COPY --from=goose /go/bin/goose /usr/local/bin/goose
COPY services/telemetry-store/migrations/*.sql /migrations/
EXPOSE 8080
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/telemetry-store"]
