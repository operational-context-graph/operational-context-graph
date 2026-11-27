# syntax=docker/dockerfile:1@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

# Build context must be the repository root.
# Example: docker build -f services/telemetry-store/Dockerfile .
# Via Bake:  docker buildx bake telemetry-store

ARG GO_IMAGE="golang:1.27@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666"
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
