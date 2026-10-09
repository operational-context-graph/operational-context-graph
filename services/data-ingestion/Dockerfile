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
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/data-ingestion /usr/local/bin/data-ingestion
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/data-ingestion"]
