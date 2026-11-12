SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
BIN_DIR      := $(CURDIR)/bin
COVERAGE_DIR ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD ?= 20
SCRIPTS_DIR  := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))../../scripts)

.PHONY: build run vet test test-coverage lint tidy

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

test-coverage:
	$(SCRIPTS_DIR)/check-coverage.sh $(COVERAGE_DIR)/$(NAME).out $(COVERAGE_THRESHOLD)

lint:
	golangci-lint run

tidy:
	go mod tidy
