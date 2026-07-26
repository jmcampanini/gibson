.PHONY: help build test lint lint-fix fmt fmt-check tidy tidy-check check clean

BUILD_DIR   := build
BINARY      := $(BUILD_DIR)/gibson
CMD         := .
PKG         := ./...
GOFMT_FILES := $(shell git ls-files --cached --others --exclude-standard '*.go' | while IFS= read -r file; do [ -f "$$file" ] && printf '%s\n' "$$file"; done)

VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X github.com/jmcampanini/gibson/cmd.Version=$(VERSION)"

.DEFAULT_GOAL := help

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ { printf "  %-16s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Build Gibson into ./build/gibson.
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

test: ## Run tests with the race detector.
	go test -race $(PKG)

lint: ## Run golangci-lint.
	golangci-lint run $(PKG)

lint-fix: ## Run golangci-lint with --fix.
	golangci-lint run --fix $(PKG)

fmt: ## Format Go files.
	@if [ -n "$(GOFMT_FILES)" ]; then gofmt -w $(GOFMT_FILES); fi

fmt-check: ## Fail if Go files need gofmt.
	@files="$$(gofmt -l $(GOFMT_FILES))"; \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; \
		echo "$$files"; \
		echo "Run: make fmt"; \
		exit 1; \
	fi

tidy: ## Apply go mod tidy.
	go mod tidy

tidy-check: ## Fail if go mod tidy would change dependency metadata.
	@out=$$(go mod tidy -diff); rc=$$?; \
	if [ $$rc -eq 0 ]; then exit 0; fi; \
	if [ -n "$$out" ]; then echo "$$out"; echo "go mod tidy would change go.mod/go.sum"; exit 1; fi; \
	echo "go mod tidy failed (rc=$$rc)"; exit $$rc

check: fmt-check tidy-check lint test ## Run all non-mutating checks.

clean: ## Remove build artifacts, coverage files, and test cache.
	rm -rf $(BUILD_DIR) coverage.out coverage.html *.coverprofile
	go clean -testcache
