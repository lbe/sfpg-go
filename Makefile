SHELL := /bin/bash

# Default package for tests/benchmarks. Override with: make test PKG=./...
PKG ?= ./internal/server

STAMP := $(shell date +%Y%m%d-%H%M%S)
BENCH_DIR := bench
BENCH_OUT := $(BENCH_DIR)/server-bench-$(STAMP).txt

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  make test          - Run tests for $(PKG)"
	@echo "  make test-race     - Run tests with race detector"
	@echo "  make test-all      - Run tests across all packages"
	@echo "  make lint          - Run golangci-lint"
	@echo "  make cover         - Generate coverage report"
	@echo "  make bench         - Run benchmarks (single iteration)"
	@echo "  make bench5        - Run benchmarks (5 iterations)"
	@echo "  make build         - Build the binary"
	@echo "  make run           - Build and run the server"
	@echo "  make format        - Format Go, templates, and assets"
	@echo "  make format-check  - Check formatting without writing"
	@echo "  make validate-templates - Validate Go template rendering + Hyperscript"
	@echo "  make validate-hyperscript - Validate Hyperscript in templates"
	@echo "  make clean         - Remove build artifacts"
	@echo ""
	@echo "Environment variables:"
	@echo "  PKG=<path>         - Override package (default: $(PKG))"

.PHONY: test
test:
	# Run all tests with session env for compatibility
	SEPG_SESSION_SECURE=false go test -tags "integration" $(PKG) -v -count=1

.PHONY: test-race
test-race:
	# Run all tests with race detector
	SEPG_SESSION_SECURE=false go test -tags "integration" $(PKG) -race -count=1

.PHONY: test-all
test-all:
	# Run tests across all packages
	SEPG_SESSION_SECURE=false go test -tags "integration" ./... -count=1

.PHONY: lint
lint:
	# Run golangci-lint across all packages
	golangci-lint run --max-same-issues 0 ./...

.PHONY: cover
cover:
	# Generate coverage profile and HTML report
	SEPG_SESSION_SECURE=false go test -tags "integration" ./... -coverprofile=coverage.out -count=1
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -n 1
	@echo "Coverage report: coverage.html"

.PHONY: bench
bench:
	@mkdir -p $(BENCH_DIR)
	# Run only benchmarks (skip tests), include mem stats, and allow HTTP cookies in tests
	SEPG_SESSION_SECURE=false go test $(PKG) -run ^$$ -bench . -benchmem -count=1 | tee "$(BENCH_OUT)"
	@echo "Saved: $(BENCH_OUT)"

.PHONY: bench5
bench5:
	@mkdir -p $(BENCH_DIR)
	# Run each benchmark 5 times for stability; save last run
	SEPG_SESSION_SECURE=false go test $(PKG) -run ^$$ -bench . -benchmem -count=5 | tee "$(BENCH_OUT)"
	@echo "Saved: $(BENCH_OUT)"

# Allow cross-compilation via GOOS/GOARCH
GOOS ?= $$(go env GOOS)
GOARCH ?= $$(go env GOARCH)
BINARY_NAME := sfpg-$(GOOS)-$(GOARCH)

.PHONY: build
build:
	# Build the application binary (respects GOOS/GOARCH for cross-compilation)
	go generate ./...
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -v -o $(BINARY_NAME) .

.PHONY: run
run: build
	# Run the application (requires SEPG_SESSION_SECRET)
	./$(BINARY_NAME)

.PHONY: clean
clean:
	# Remove build artifacts (including cross-compiled binaries)
	rm -f sfpg-* coverage.out coverage.html
	@echo "Cleaned build artifacts"

.PHONY: validate-hyperscript
validate-hyperscript:
	# Validate Hyperscript syntax across templates
	go run ./scripts/validate-hyperscript.go web/templates

.PHONY: validate-templates
validate-templates:
	# Fast-fail checks for template integrity and embedded hyperscript
	SEPG_SESSION_SECURE=false go test ./internal/server -run TestTemplateRendering -count=1
	go run ./scripts/validate-hyperscript.go -quiet web/templates

.PHONY: format fmt
format fmt:
	# Format Go source files with gofmt and goimports
	@git ls-files '*.go' | grep -Ev '^(tmp/|zarchive/)' | xargs gofmt -w
	@git ls-files '*.go' | grep -Ev '^(tmp/|zarchive/)' | xargs goimports -w
	# Format templates, styles, scripts, markdown, yaml, etc. via Prettier
	npx --yes prettier --write .

.PHONY: format-check fmt-check
format-check fmt-check:
	# Check Go formatting with gofmt (fails if any file needs formatting)
	@unformatted=$$(git ls-files '*.go' | grep -Ev '^(tmp/|zarchive/)' | xargs gofmt -l 2>/dev/null | sort -u); \
	if [[ -n "$$unformatted" ]]; then \
		echo "Unformatted Go files:"; echo "$$unformatted"; exit 1; \
	else \
		echo "Go files are properly formatted."; \
	fi
	# Check Go imports with goimports
	@git ls-files '*.go' | grep -Ev '^(tmp/|zarchive/)' | xargs goimports -l 2>/dev/null | grep . && exit 1 || echo "Go imports are correct."
	# Check Prettier formatting
	npx --yes prettier --check .
