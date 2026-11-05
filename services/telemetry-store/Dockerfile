# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root.
# Example: docker build -f services/telemetry-store/Dockerfile .
# Via Bake:  docker buildx bake telemetry-store

ARG GO_IMAGE="golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY services/telemetry-store/go.mod services/telemetry-store/go.sum services/telemetry-store/
RUN cd services/telemetry-store && go mod download

COPY services/telemetry-store/ services/telemetry-store/
RUN CGO_ENABLED=0 go build -trimpath -o /out/telemetry-store \
    ./services/telemetry-store/cmd/telemetry-store

FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/telemetry-store /usr/local/bin/telemetry-store
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/telemetry-store"]
