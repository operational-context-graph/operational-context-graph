# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /telemetry-store ./cmd/server

FROM gcr.io/distroless/static:nonroot
COPY --from=build /telemetry-store /telemetry-store
EXPOSE 8081
ENTRYPOINT ["/telemetry-store"]
