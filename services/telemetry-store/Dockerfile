# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

# OCG Telemetry Store service image
#
# Build:
#   docker build -f services/telemetry-store/Dockerfile \
#     --build-arg VERSION=1.0.0 \
#     -t ghcr.io/operational-context-graph/telemetry-store:1.0.0 \
#     services/telemetry-store
#
# Port:
#   8080 — HTTP API

FROM golang:1.27 AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/telemetry-store \
    ./cmd/telemetry-store

FROM gcr.io/distroless/static-debian12

COPY --from=builder /out/telemetry-store /telemetry-store

USER 1000:1000

EXPOSE 8080

ENTRYPOINT ["/telemetry-store"]
