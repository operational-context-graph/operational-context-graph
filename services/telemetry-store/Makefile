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
	@go test ./... -race -count=1 -json -coverprofile=$(COVERAGE_DIR)/$(NAME).out \
	  | go tool tparse; test_rc=$${PIPESTATUS[0]}; \
	go tool cover -html=$(COVERAGE_DIR)/$(NAME).out -o $(COVERAGE_DIR)/$(NAME).html 2>/dev/null; \
	printf "  coverage report: \033[4mfile://$(COVERAGE_DIR)/$(NAME).html\033[0m\n"; \
	exit $$test_rc

lint:
	golangci-lint run

tidy:
	go mod tidy
