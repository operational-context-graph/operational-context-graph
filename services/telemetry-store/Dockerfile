# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker build -f services/telemetry-store/Dockerfile .
FROM golang:1.27.3 AS build
WORKDIR /src

COPY services/telemetry-store/go.mod services/telemetry-store/go.sum services/telemetry-store/
RUN cd services/telemetry-store && go mod download

COPY services/telemetry-store/ services/telemetry-store/
RUN cd services/telemetry-store && CGO_ENABLED=0 go build -trimpath -o /out/telemetry-store \
    ./cmd/telemetry-store

# Runtime stage.
FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/telemetry-store /usr/local/bin/telemetry-store
EXPOSE 8080
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/telemetry-store"]
