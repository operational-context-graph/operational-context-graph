# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# Hardened Apache Doris Backend image
# Base: apache/doris:be-4.1.4 (Ubuntu 22.04)
#
# Hardening applied:
#   1. Pinned upstream tag. Use image digest in production:
#        FROM apache/doris:be-4.1.4@sha256:<digest>
#      Obtain: docker inspect --format='{{index .RepoDigests 0}}' apache/doris:be-4.1.4
#   2. Build/debug tooling removed (gcc, g++, cpp, binutils, gdb, strace).
#   3. Runs as non-root doris user (created by upstream image).
#   4. be.conf baked in with high-throughput ingestion and compaction settings
#      required by the Telemetry Store SDD Deployment View 3.4.
#
# Known remaining surface — production prerequisites:
#   - Java library advisories in BE JNI components (jackson, etc.): upgrade JARs.
#
# Host prerequisites (not settable inside the container):
#   sysctl vm.max_map_count >= 2000000
#   Swap disabled (swapoff -a)
#
# Build:
#   docker build -f infrastructure/doris/Dockerfile.be -t ghcr.io/operational-context-graph/doris-be:latest .
#
# Ports:
#   9060 — BE Thrift (query fragment execution)
#   8040 — BE HTTP (status, health check, Stream Load target)
#   9050 — Heartbeat service (FE ↔ BE liveness)
#   8060 — BRPC (high-speed FE ↔ BE data exchange)

FROM apache/doris:be-4.1.4

USER root

RUN apt-get update --quiet && \
    apt-get remove -y --auto-remove \
        gcc \
        g++ \
        cpp \
        binutils \
        gdb \
        strace \
        linux-headers-generic \
        2>/dev/null || true && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY infrastructure/doris/be.conf /opt/apache-doris/be/conf/be.conf

USER doris

EXPOSE 9060 8040 9050 8060
