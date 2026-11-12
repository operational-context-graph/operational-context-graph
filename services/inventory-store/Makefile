SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
COVERAGE_DIR ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD ?= 20
SCRIPTS_DIR  := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))../../scripts)

.PHONY: build run vet test test-coverage lint tidy

build:
	go build ./...

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
