# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

# Hardened PostgreSQL image for the OCG Inventory Store
# Base: postgres:16 (Debian Bookworm)
#
# Alternative: postgres:16-alpine has a significantly smaller surface by default
# (Alpine ships almost no extra tooling). Switch the FROM line and remove the
# apt-get block if the Alpine variant is chosen. Verify no glibc dependency
# exists before switching (Alpine uses musl libc).
#
# Hardening applied:
#   1. Pinned upstream tag. Use image digest in production:
#        FROM postgres:16@sha256:<digest>
#      Obtain: docker inspect --format='{{index .RepoDigests 0}}' postgres:16
#   2. Build/debug tooling removed (gcc, g++, cpp, binutils, make, gdb, strace, ltrace).
#   3. postgresql.conf and pg_hba.conf baked in (scram-sha-256 auth enforced via pg_hba.conf).
#   4. Runs as non-root postgres user (default in upstream image, made explicit).
#
# Note: init.sql is NOT baked in — it is mounted at runtime via
# /docker-entrypoint-initdb.d/ so the schema stays in source control and
# the image does not need rebuilding on every schema change.
#
# Build:
#   docker build -f infrastructure/postgres/Dockerfile -t ghcr.io/operational-context-graph/postgres:latest .
#
# Port:
#   5432 — PostgreSQL wire protocol

FROM postgres:16

USER root

RUN apt-get update --quiet && \
    apt-get remove -y --auto-remove \
        gcc \
        g++ \
        cpp \
        binutils \
        make \
        gdb \
        strace \
        ltrace \
        2>/dev/null || true && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY infrastructure/postgres/postgresql.conf /etc/postgresql/postgresql.conf
COPY infrastructure/postgres/pg_hba.conf     /etc/postgresql/pg_hba.conf

RUN chown postgres:postgres \
    /etc/postgresql/postgresql.conf \
    /etc/postgresql/pg_hba.conf

USER postgres

EXPOSE 5432

CMD ["postgres", \
     "-c", "config_file=/etc/postgresql/postgresql.conf", \
     "-c", "hba_file=/etc/postgresql/pg_hba.conf"]
