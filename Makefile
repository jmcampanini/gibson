.DEFAULT_GOAL := help

BUILD_DIR := build
BINARY := $(BUILD_DIR)/gibson
WEB_DIR := web
WEB_INSTALL_MARK := $(WEB_DIR)/node_modules/.package-lock.json
VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || printf 'unknown')
LDFLAGS := -ldflags "-X github.com/jmcampanini/gibson/cmd.Version=$(VERSION)"

.PHONY: help dev-web dev-server web build test cli-proof lint lint-fix fmt fmt-check tidy tidy-check version-check vuln check clean

help: ## Show available targets.
	@printf 'Usage: make <target>\n\nTargets:\n'
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | LC_ALL=C sort

$(WEB_INSTALL_MARK): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	npm ci --prefix $(WEB_DIR)

dev-web: $(WEB_INSTALL_MARK) ## Run the Vite development server.
	npm run dev --prefix $(WEB_DIR)

dev-server: ## Run Gibson with the Vite development proxy.
	go run $(LDFLAGS) . serve --dev

web: $(WEB_INSTALL_MARK) ## Build the production web application.
	npm run build --prefix $(WEB_DIR)

build: web ## Build build/gibson with git-derived version metadata.
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -buildvcs=false $(LDFLAGS) -o $(BINARY) .

test: ## Run all tests uncached with the race detector.
	go test -count=1 -race ./...

cli-proof: ## Build and verify the compiled CLI contract.
	@$(MAKE) --no-print-directory build VERSION=cli-proof
	@BINARY=$(BINARY) ./scripts/cli-proof.sh

lint: ## Run static analysis.
	go tool golangci-lint run

lint-fix: ## Run static analysis with --fix.
	go tool golangci-lint run --fix

fmt: ## Format Go source files.
	go tool golangci-lint fmt

fmt-check: ## Verify formatting without changing files.
	go tool golangci-lint fmt --diff

tidy: ## Apply go mod tidy.
	go mod tidy

tidy-check: ## Verify go.mod and go.sum are tidy without changing them.
	go mod tidy -diff

version-check: build ## Verify the built binary reports the injected version.
	@case "$(VERSION)" in unknown|n/a|"") echo "degenerate version identity: '$(VERSION)'"; exit 1;; esac
	@out="$$($(BINARY) --version)"; \
	if [ "$$out" != "gibson version $(VERSION)" ]; then \
		echo "version mismatch: got '$$out', want 'gibson version $(VERSION)'"; \
		exit 1; \
	fi

vuln: ## Check dependencies and reachable code for known vulnerabilities.
	go tool govulncheck ./...

check: fmt-check tidy-check lint test build version-check cli-proof vuln ## Run the complete local verification contract.

clean: ## Remove build artifacts, coverage files, and test cache.
	rm -rf $(BUILD_DIR) $(WEB_DIR)/dist coverage.out coverage.html *.coverprofile
	go clean -testcache
