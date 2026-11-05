# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root.
# Example: docker buildx build --build-arg VERSION=1.0.0 -t ghcr.io/operational-context-graph/inventory-store:1.0.0 -f services/inventory-store/Dockerfile .
# Via Bake:  docker buildx bake inventory-store
#
# Port:
#   8080 — HTTP API

ARG GO_IMAGE="golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"
ARG VERSION=dev

FROM ${GO_IMAGE} AS build
ARG VERSION
WORKDIR /src

COPY services/inventory-store/go.mod services/inventory-store/go.sum services/inventory-store/
RUN cd services/inventory-store && go mod download

COPY services/inventory-store/ services/inventory-store/
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/inventory-store \
    ./services/inventory-store/cmd/inventory-store

FROM ${GO_IMAGE} AS goose
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.29.0

FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/inventory-store /inventory-store
COPY --from=goose /go/bin/goose /usr/local/bin/goose
COPY services/inventory-store/internal/migrations/*.sql /migrations/
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/inventory-store"]
