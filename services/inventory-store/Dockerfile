# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

# OCG Inventory Store service image
#
# Build:
#   docker build -f services/inventory-store/Dockerfile \
#     --build-arg VERSION=1.0.0 \
#     -t ghcr.io/operational-context-graph/inventory-store:1.0.0 \
#     services/inventory-store
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
    -o /out/inventory-store \
    ./cmd/inventory-store

FROM gcr.io/distroless/static-debian12

COPY --from=builder /out/inventory-store /inventory-store

USER 1000:1000

EXPOSE 8080

ENTRYPOINT ["/inventory-store"]
