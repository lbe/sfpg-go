SHELL := /bin/bash

# Default package for tests/benchmarks. Override with: make test PKG=./...
PKG ?= ./internal/server

STAMP := $(shell date +%Y%m%d-%H%M%S)
BENCH_DIR := bench
BENCH_OUT := $(BENCH_DIR)/server-bench-$(STAMP).txt

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  make bench         - Run benchmarks (single iteration)"
	@echo "  make bench5        - Run benchmarks (5 iterations)"
	@echo "  make build         - Build the binary"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make cover         - Generate coverage report"
	@echo "  make format        - Format Go, templates, and assets"
	@echo "  make format-check  - Check formatting without writing"
	@echo "  make run           - Build and run the server"
	@echo "  make test          - Run tests for $(PKG)"
	@echo "  make test-all      - Run tests across all packages"
	@echo "  make test-race     - Run tests with race detector"
	@echo "  make lint          - Run golangci-lint"
	@echo "  make perf-test     - Run full performance test suite"
	@echo "  make perf-test-help - Show performance testing commands"
	@echo "  make validate-templates - Validate Go template rendering + Hyperscript"
	@echo "  make validate-hyperscript - Validate Hyperscript in templates"
	@echo ""
	@echo "Environment variables:"
	@echo "  PKG=<path>         - Override package (default: $(PKG))"

.PHONY: test
test:
	# Run all tests with session env for compatibility
	# Extra go test flags can be passed via ARGS: make test ARGS="-count=1 -v"
	time SEPG_SESSION_SECURE=false go test -tags "integration" $(PKG) $(ARGS)

.PHONY: test-race
test-race:
	# Run all tests with race detector
	time SEPG_SESSION_SECURE=false go test -tags "integration" $(PKG) -race $(ARGS)

.PHONY: test-all
test-all:
	# Run tests across all packages
	time SEPG_SESSION_SECURE=false go test -tags "integration" ./... $(ARGS)

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
clean: perf-test-clean
	# Remove build artifacts (including cross-compiled binaries)
	rm -f sfpg-* coverage.out coverage.html
	@echo "Cleaned build artifacts"
	# Clean tmp/: remove all files/dirs except Images/, DB/, and *.yaml/*.yml configs
	@find tmp/ -mindepth 1 -maxdepth 1 \
		! -name 'Images' \
		! -name 'DB' \
		! -name '*.yaml' \
		! -name '*.yml' \
		-exec rm -rf {} +
	@echo "Cleaned tmp/ (preserved: Images/, DB/, *.yaml, *.yml)"

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

# Performance testing targets
.PHONY: perf-test
perf-test: perf-test-setup
	@echo ""
	@echo "═══════════════════════════════════════════════════════════════"
	@echo "Running performance test suite (vegeta)"
	@echo "═══════════════════════════════════════════════════════════════"
	@echo ""
	@chmod +x scripts/perf-test/*.sh
	./scripts/perf-test/run-baseline.sh
	./scripts/perf-test/run-sustained.sh
	./scripts/perf-test/run-mixed-workload.sh
	./scripts/perf-test/analyze-results.sh
	@echo ""
	@echo "═══════════════════════════════════════════════════════════════"
	@echo "✓ Performance testing complete!"
	@echo "═══════════════════════════════════════════════════════════════"
	@echo ""
	@echo "Summary reports generated in results/"
	@echo "  See: results/perf-report-final.txt"
	@echo ""

.PHONY: perf-test-setup
perf-test-setup:
	@echo "Setting up performance test data..."
	@chmod +x scripts/perf-test/*.sh
	@mkdir -p results tmp
	./scripts/perf-test/setup-test-data.sh

.PHONY: perf-gen-attacks
perf-gen-attacks:
	@chmod +x scripts/perf-test/gen-attack-files.sh
	./scripts/perf-test/gen-attack-files.sh

.PHONY: perf-test-compare-cache
perf-test-compare-cache:
	@echo "Running cache impact comparison test..."
	@chmod +x scripts/perf-test/compare-cache-impact.sh
	./scripts/perf-test/compare-cache-impact.sh

.PHONY: perf-test-clean
perf-test-clean:
	@echo "Cleaning up performance test artifacts..."
	rm -rf results/
	rm -f tmp/warmup.bin tmp/mixed-light.bin
	@echo "✓ Performance test artifacts cleaned"

.PHONY: perf-test-help
perf-test-help:
	@echo "Performance Testing Commands:"
	@echo ""
	@echo "  make perf-test              - Run full test suite (setup + all tests)"
	@echo "  make perf-test-setup        - Generate attack files and warm cache"
	@echo "  make perf-gen-attacks       - (Re)generate attack files from live DB only"
	@echo "  make perf-test-compare-cache - Compare cache on vs off impact"
	@echo "  make perf-test-clean        - Clean test results"
	@echo "  make perf-test-help         - Show this help"
	@echo ""
	@echo "Manual test commands:"
	@echo "  ./scripts/perf-test/gen-attack-files.sh    - Generate attack files from DB"
	@echo "  ./scripts/perf-test/run-baseline.sh        - Baseline tests (cache enabled)"
	@echo "  ./scripts/perf-test/run-sustained.sh       - Sustained load tests"
	@echo "  ./scripts/perf-test/run-mixed-workload.sh  - Mixed workload test"
	@echo "  ./scripts/perf-test/compare-cache-impact.sh - Cache on vs off"
	@echo "  ./scripts/perf-test/analyze-results.sh     - Parse and display results"
	@echo ""
	@echo "Documentation:"
	@echo "  See: scripts/perf-test/PERFORMANCE_TESTING.md"
	@echo ""
