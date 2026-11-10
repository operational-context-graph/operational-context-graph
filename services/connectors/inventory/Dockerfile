# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker buildx build --platform linux/amd64 -t ghcr.io/operational-context-graph/inventory-connector:latest -f services/connectors/inventory/Dockerfile .
# Via Bake:  docker buildx bake inventory-connector

ARG GO_IMAGE="golang:1.27@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e"
ARG DISTROLESS_IMAGE="gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/connectors/inventory/go.mod services/connectors/inventory/go.sum services/connectors/inventory/
RUN go work init gen/go services/connectors/inventory && go mod download 2>/dev/null

COPY gen/go/ gen/go/
COPY services/connectors/inventory/ services/connectors/inventory/
RUN CGO_ENABLED=0 go build -trimpath -o /out/inventory-connector \
    github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/cmd/connector

FROM ${DISTROLESS_IMAGE}
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/inventory-connector /usr/local/bin/inventory-connector
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/inventory-connector"]
