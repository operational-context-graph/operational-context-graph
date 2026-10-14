# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# syntax=docker/dockerfile:1

# Build stage.
FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/data-ingestion ./cmd/data-ingestion

# Runtime stage.
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/data-ingestion /usr/local/bin/data-ingestion
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/data-ingestion"]
