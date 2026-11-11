SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
COVERAGE_DIR ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD ?= 20

.PHONY: build run vet test test-coverage lint tidy

build:
	go build ./...

run:
	go run ./cmd/connector

vet:
	go vet ./...

test:
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/$(NAME).out
	$(MAKE) test-coverage

test-coverage:
	../../../scripts/check-coverage.sh $(COVERAGE_DIR)/$(NAME).out $(COVERAGE_THRESHOLD)

lint:
	golangci-lint run

tidy:
	go mod tidy
