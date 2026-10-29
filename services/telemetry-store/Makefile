# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0

SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
BIN_DIR      := $(CURDIR)/bin
COVERAGE_DIR ?= $(CURDIR)/coverage

.PHONY: build run vet test lint tidy

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(NAME) ./cmd/$(NAME)

run:
	go run ./cmd/$(NAME)

vet:
	go vet ./...

test:
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/$(NAME).out

lint:
	golangci-lint run

tidy:
	go mod tidy
