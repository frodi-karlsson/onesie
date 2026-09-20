BINARY  := jev
PKG     := ./cmd/jev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: help build run install test test-race cover bench lint lint-fix fmt tidy vuln check tools clean

build: ## Build the jev binary into bin/
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

run: ## Build and run the binary, for example make run ARGS="version"
	@$(MAKE) --no-print-directory build
	@./bin/$(BINARY) $(ARGS)

install: ## Install jev to $(GOPATH)/bin
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

test: ## Run unit tests
	go test ./... $(TESTARGS)

test-race: ## Run unit tests with the race detector
	go test -race ./...

cover: ## Run tests with coverage and open the HTML report
	@mkdir -p bin
	go test -coverprofile=bin/coverage.out -covermode=atomic ./...
	go tool cover -func=bin/coverage.out | tail -1
	go tool cover -html=bin/coverage.out -o bin/coverage.html
	@echo "report: bin/coverage.html"

bench: ## Run benchmarks
	go test -run '^$$' -bench . -benchmem ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

lint-fix: ## Run golangci-lint with auto fixes, then format
	golangci-lint run --fix ./...
	golangci-lint fmt ./...

fmt: ## Format all Go code
	golangci-lint fmt ./...

tidy: ## Tidy and verify module dependencies
	go mod tidy
	go mod verify

vuln: ## Scan dependencies for known vulnerabilities
	go tool govulncheck ./...

check: ## Run lint and tests
	@echo "--- Lint ---"
	@$(MAKE) --no-print-directory lint
	@echo ""
	@echo "--- Tests ---"
	@$(MAKE) --no-print-directory test-race

tools: ## Install the pinned developer tools into bin/
	GOBIN=$(CURDIR)/bin go install tool

clean: ## Remove build artifacts
	rm -rf bin
	go clean -testcache

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
