# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Build context must be the repository root so go.work resolves gen/go.
# Example: docker buildx build --platform linux/amd64 --provenance=false -t ghcr.io/operational-context-graph/inventory-connector:latest -f services/connectors/inventory/Dockerfile .
FROM golang:1.27@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
WORKDIR /src

COPY gen/go/go.mod gen/go/go.sum gen/go/
COPY services/connectors/inventory/go.mod services/connectors/inventory/go.sum services/connectors/inventory/
RUN go work init gen/go services/connectors/inventory && go mod download -x 2>/dev/null; true

COPY gen/go/ gen/go/
COPY services/connectors/inventory/ services/connectors/inventory/
RUN CGO_ENABLED=0 go build -trimpath -o /out/inventory-connector \
    github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/cmd/connector

# Runtime stage.
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
LABEL org.opencontainers.image.source=https://github.com/operational-context-graph/operational-context-graph
COPY --from=build /out/inventory-connector /usr/local/bin/inventory-connector
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/inventory-connector"]
