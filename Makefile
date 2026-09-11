# Copyright (c) 2026, Sukkaito. All rights reserved.

GO ?= go
BINARY_NAME := dcgm-exporter-supervisor
VERSION ?= 0.1.0

.DEFAULT_GOAL := all

.PHONY: all binary test fmt check-fmt clean

all: binary

binary: ## Build the dcgm-exporter-supervisor binary
	$(GO) build -trimpath -ldflags "-X main.BuildVersion=$(VERSION)" -o bin/$(BINARY_NAME) ./cmd/dcgm-exporter-supervisor

test: ## Run unit tests
	$(GO) test -v -count=1 ./...

fmt: ## Format Go source files
	$(GO) fmt ./...

check-fmt: ## Check code formatting
	test $$(gofmt -l . | wc -l) -eq 0

clean: ## Clean build artifacts
	rm -rf bin/

