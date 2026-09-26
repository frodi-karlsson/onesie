BINARY  := onesie
PKG     := ./cmd/onesie
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# Not TAG, which make release TAG= passes down to every sub make.
EXACT_TAG ?= $(shell git diff --quiet HEAD 2>/dev/null && git describe --tags --exact-match 2>/dev/null)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE) \
	-X main.tag=$(EXACT_TAG)

.PHONY: help build run install test test-race cover bench lint lint-fix fmt tidy vuln check tools clean skills skills-check skills-eval fuzz fuzz-check release examples-answers schema

build: ## Build the onesie binary into bin/
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

run: ## Build and run the binary, for example make run ARGS="version"
	@$(MAKE) --no-print-directory build
	@./bin/$(BINARY) $(ARGS)

install: ## Install onesie to $(GOPATH)/bin
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

test: ## Run unit tests
	go test -timeout 2m ./... $(TESTARGS)

test-race: ## Run unit tests with the race detector
	go tool gotestsum --format testname -- -race -timeout 2m ./...

test-integration: ## Run tests against the live API. Needs TYPESAFE_API_KEY, OPENROUTER_API_KEY and BERGET_API_KEY for their cases, or a .env
	go test -tags integration -race -count=1 -timeout 5m ./internal/jev/ ./internal/cli/ -run 'TestLive|Integration' -v

FUZZ_TARGETS := \
	./internal/assert/:FuzzParse \
	./internal/output/:FuzzCodeSpan \
	./internal/output/:FuzzMarkdownTable \
	./internal/calibrate/:FuzzSplitLabel \
	./internal/calibrate/:FuzzParseLabel \
	./internal/calibrate/:FuzzParseCuts \
	./internal/calibrate/:FuzzParseRequirement \
	./internal/qfile/:FuzzIsName \
	./internal/qfile/:FuzzFind \
	./internal/jq/:FuzzID \
	./internal/cli/:FuzzPrintQuestions \
	./internal/cli/:FuzzPrintQuestionsJSON \
	./internal/cli/:FuzzLineVerdict \
	./internal/release/:FuzzParseTag \
	./internal/mock/:FuzzLoad \
	./internal/cache/:FuzzDecodeEntry

fuzz-check: ## Check that FUZZ_TARGETS lists every fuzz target in the tree and nothing else
	@found=$$(grep -rEo --include='*_test.go' --exclude-dir=.git --exclude-dir=docs '^func Fuzz[A-Za-z0-9_]+' . | \
		sed -E 's|^(.*)/[^/]+_test\.go:func (Fuzz.*)$$|\1/:\2|'); \
	status=0; \
	for target in $$found; do \
		case " $(FUZZ_TARGETS) " in *" $$target "*) ;; *) echo "missing from FUZZ_TARGETS: $$target"; status=1 ;; esac; \
	done; \
	for target in $(FUZZ_TARGETS); do \
		case " $$(echo $$found) " in *" $$target "*) ;; *) echo "in FUZZ_TARGETS but not in the tree: $$target"; status=1 ;; esac; \
	done; \
	exit $$status

fuzz: ## Fuzz every parser and escaper in turn, FUZZTIME=1m each by default
	@set -e; for target in $(FUZZ_TARGETS); do \
		echo "--- $${target##*:} ---"; \
		go test $${target%%:*} -run '^$$' -fuzz "^$${target##*:}$$" -fuzztime $(or $(FUZZTIME),1m) -fuzzminimizetime 0; \
	done

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

schema: ## Regenerate schema/questions.json
	go run ./cmd/onesie --print-schema > schema/questions.json

examples-answers: ## Regenerate the starter answers files, run by hand with a key
	go run ./cmd/examplesanswers

skills: ## Generate the per client skill files from skills/
	go run ./cmd/skillgen

skills-check: build ## Dry run every example in every skill, against the binary built from this tree
	PATH="$(CURDIR)/bin:$$PATH" go run ./cmd/skillcheck

skills-eval: ## Measure the skills against an agent, run by hand
	@$(MAKE) --no-print-directory build
	claude plugin eval . --ablation with-without --keep-temp --no-publish --threshold 0 $(EVALARGS)
	PATH="$(CURDIR)/bin:$$PATH" go run ./cmd/skilleval

check: ## Run the fuzz target check, lint and tests
	@echo "--- Fuzz targets ---"
	@$(MAKE) --no-print-directory fuzz-check
	@echo ""
	@echo "--- Lint ---"
	@$(MAKE) --no-print-directory lint
	@echo ""
	@echo "--- Tests ---"
	@$(MAKE) --no-print-directory test-race

# Named through RELEASE_MAKE so make -n only prints the release line. Make
# runs a recipe line that names $(MAKE) directly even under -n.
RELEASE_MAKE = $(MAKE)

release: ## Cut a release, for example make release TAG=v0.2.0, with DRY_RUN=1 to only check
	@test -n "$(TAG)" || { echo "make release needs TAG, for example TAG=v0.2.0"; exit 2; }
	MAKE='$(RELEASE_MAKE)' DRY_RUN='$(DRY_RUN)' bash scripts/release.sh '$(TAG)'

tools: ## Install the pinned developer tools into bin/
	GOBIN=$(CURDIR)/bin go install tool

clean: ## Remove build artifacts
	rm -rf bin
	go clean -testcache

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
