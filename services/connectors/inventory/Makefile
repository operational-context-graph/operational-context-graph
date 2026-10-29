# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
COVERAGE_DIR ?= $(CURDIR)/coverage

.PHONY: build run vet test lint tidy

build:
	go build ./...

run:
	go run ./cmd/connector

vet:
	go vet ./...

test:
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/$(NAME).out

lint:
	golangci-lint run

tidy:
	go mod tidy
