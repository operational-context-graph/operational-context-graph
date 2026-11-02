# OCG Inventory Store service image
#
# Build:
#   docker build -f services/inventory-store/Dockerfile \
#     --build-arg VERSION=1.0.0 \
#     -t ghcr.io/operational-context-graph/inventory-store:1.0.0 \
#     services/inventory-store
#
# Port:
#   8080 — HTTP API

FROM golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/inventory-store \
    ./cmd/inventory-store

# Goose stage — build the migration CLI independently so it does not affect go.mod.
FROM golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS goose
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@v3.29.0

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2

COPY --from=builder /out/inventory-store /inventory-store
COPY --from=goose /go/bin/goose /usr/local/bin/goose
COPY internal/migrations/*.sql /migrations/

USER 1000:1000

EXPOSE 8080

ENTRYPOINT ["/inventory-store"]
