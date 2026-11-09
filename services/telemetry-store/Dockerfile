# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root.
# Example: docker build -f services/telemetry-store/Dockerfile .
# Via Bake:  docker buildx bake telemetry-store

ARG GO_IMAGE="golang:1.27@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY services/telemetry-store/go.mod services/telemetry-store/go.sum services/telemetry-store/
RUN cd services/telemetry-store && go mod download

COPY services/telemetry-store/ services/telemetry-store/
RUN cd services/telemetry-store && CGO_ENABLED=0 go build -trimpath -o /out/telemetry-store \
    ./cmd/telemetry-store

FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/telemetry-store /usr/local/bin/telemetry-store
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/telemetry-store"]
