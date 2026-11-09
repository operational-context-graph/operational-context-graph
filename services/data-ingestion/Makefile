SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
COVERAGE_DIR ?= $(CURDIR)/coverage

.PHONY: build run vet test lint tidy mocks

build:
	go build ./...

run:
	go run ./cmd/$(NAME)

vet:
	go vet ./...

test:
	@"$$(git rev-parse --show-toplevel)/scripts/go-test.sh" $(NAME) $(COVERAGE_DIR)

lint:
	golangci-lint run

tidy:
	go mod tidy

# Requires mockery; installed as a Go tool in go.mod.
mocks:
	go tool mockery
