# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker build -f services/data-ingestion/Dockerfile .
# Via Bake:  docker buildx bake data-ingestion

ARG GO_IMAGE="golang:1.27@sha256:ec47ea3725fea0cc88f6de94919aef19f40f12fc1ec0e2d1b3426dbf15ecca04"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/data-ingestion/go.mod services/data-ingestion/go.sum services/data-ingestion/
RUN go work init gen/go services/data-ingestion && go mod download 2>/dev/null

COPY gen/go/ gen/go/
COPY services/data-ingestion/ services/data-ingestion/
RUN CGO_ENABLED=0 go build -trimpath -o /out/data-ingestion \
    github.com/operational-context-graph/operational-context-graph/services/data-ingestion/cmd/data-ingestion

# Goose stage — build the migration CLI independently so it does not affect go.mod.
FROM golang:1.27@sha256:ec47ea3725fea0cc88f6de94919aef19f40f12fc1ec0e2d1b3426dbf15ecca04 AS goose
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.29.0

FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/data-ingestion /usr/local/bin/data-ingestion
COPY --from=goose /go/bin/goose /usr/local/bin/goose
COPY services/data-ingestion/migrations /migrations
USER 1000:1000
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/data-ingestion"]
