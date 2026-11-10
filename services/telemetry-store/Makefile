SHELL        := /usr/bin/env bash
NAME         := $(notdir $(CURDIR))
BIN_DIR      := $(CURDIR)/bin
COVERAGE_DIR ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD ?= 25

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
	../../scripts/check-coverage.sh $(COVERAGE_DIR)/$(NAME).out $(COVERAGE_THRESHOLD)

lint:
	golangci-lint run

tidy:
	go mod tidy
