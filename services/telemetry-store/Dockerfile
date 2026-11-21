# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root.
# Example: docker build -f services/telemetry-store/Dockerfile .
# Via Bake:  docker buildx bake telemetry-store

ARG GO_IMAGE="golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY services/telemetry-store/go.mod services/telemetry-store/go.sum services/telemetry-store/
RUN cd services/telemetry-store && go mod download

COPY services/telemetry-store/ services/telemetry-store/

RUN cd services/telemetry-store && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/telemetry-store \
    ./cmd/telemetry-store

RUN cd services/telemetry-store && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate \
    ./cmd/migrate

# Goose stage — stock goose CLI for developer file-management tasks only
# (goose create, goose fix). It is not used to apply migrations to Doris.
FROM ${GO_IMAGE} AS goose
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.29.0

# Runtime stage.
FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph

COPY --from=build /out/telemetry-store /usr/local/bin/telemetry-store
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=goose /go/bin/goose /usr/local/bin/goose
COPY services/telemetry-store/internal/migrations/*.sql /migrations/

USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/telemetry-store"]
