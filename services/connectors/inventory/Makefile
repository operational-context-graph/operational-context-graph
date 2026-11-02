SHELL             := /usr/bin/env bash
NAME              := $(notdir $(CURDIR))
COVERAGE_DIR      ?= $(CURDIR)/coverage
COVERAGE_THRESHOLD ?= 95
COVERAGE_ENFORCE  ?= 1
REPO_ROOT         := $(shell git rev-parse --show-toplevel)

.PHONY: build run vet test lint tidy

build:
	go build ./...

run:
	go run ./cmd/connector

vet:
	go vet ./...

test:
	mkdir -p $(COVERAGE_DIR)
	@cd $(CURDIR) && $(REPO_ROOT)/scripts/go-test.sh \
	  $(NAME) \
	  $(COVERAGE_DIR)/$(NAME).out \
	  $(COVERAGE_THRESHOLD) \
	  $(COVERAGE_ENFORCE) \
	  $(COVERAGE_DIR)/$(NAME).html

lint:
	golangci-lint run

tidy:
	go mod tidy
