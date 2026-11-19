# syntax=docker/dockerfile:1
#
# Usage:
#   Build all services:        docker buildx bake
#   Build a single service:    docker buildx bake inventory-connector
#   Override a digest:         GO_IMAGE=golang:1.28@sha256:... docker buildx bake
#   Print resolved config:     docker buildx bake --print (for debugging)

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

variable "GO_IMAGE" {
  default = "golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b"
}

variable "DISTROLESS_IMAGE" {
  # gcr.io/distroless/static-debian12 — no shell, no package manager, minimal attack surface.
  default = "gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2"
}

variable "REGISTRY" {
  default = "ghcr.io/operational-context-graph"
}

variable "VERSION" {
  default = "dev"
}

# ---------------------------------------------------------------------------
# Shared base
# ---------------------------------------------------------------------------

target "_base" {
  context = "."
  args = {
    GO_IMAGE        = GO_IMAGE
    DISTROLESS_IMAGE = DISTROLESS_IMAGE
    VERSION         = VERSION
  }
  labels = {
    "org.opencontainers.image.source" = "https://github.com/operational-context-graph/operational-context-graph"
  }
  platforms = ["linux/amd64"]
}

# ---------------------------------------------------------------------------
# Services
# ---------------------------------------------------------------------------

target "inventory-connector" {
  inherits   = ["_base"]
  dockerfile = "services/connectors/inventory-connector/Dockerfile"
  tags       = ["${REGISTRY}/inventory-connector:${VERSION}"]
  labels = {
    "org.opencontainers.image.description" = "Ingests asset and topology data from external inventory systems into the OCG Infrastructure Knowledge Base"
  }
}

target "inventory-store" {
  inherits   = ["_base"]
  dockerfile = "services/inventory-store/Dockerfile"
  tags       = ["${REGISTRY}/inventory-store:${VERSION}"]
  labels = {
    "org.opencontainers.image.description" = "Stores data center inventory, configuration baselines, and operational knowledge for the OCG"
  }
}

target "telemetry-store" {
  inherits   = ["_base"]
  dockerfile = "services/telemetry-store/Dockerfile"
  tags       = ["${REGISTRY}/telemetry-store:${VERSION}"]
  labels = {
    "org.opencontainers.image.description" = "Ingests, deduplicates, and serves time-series operational telemetry for the OCG Analysis Engine"
  }
}

target "data-ingestion" {
  inherits   = ["_base"]
  dockerfile = "services/data-ingestion/Dockerfile"
  tags       = ["${REGISTRY}/data-ingestion:${VERSION}"]
  labels = {
    "org.opencontainers.image.description" = "Validates, normalizes, and routes connector data to the correct OCG store"
  }
}

# ---------------------------------------------------------------------------
# Groups
# ---------------------------------------------------------------------------

group "default" {
  targets = ["inventory-connector", "inventory-store", "telemetry-store", "data-ingestion"]
}
