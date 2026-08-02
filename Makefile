.PHONY: help dev-web dev-server web build test cli-proof lint lint-fix fmt fmt-check tidy tidy-check check verify clean

BUILD_DIR        := build
BINARY           := $(BUILD_DIR)/gibson
CMD              := .
PKG              := ./...
WEB_DIR          := web
WEB_INSTALL_MARK := $(WEB_DIR)/node_modules/.package-lock.json
GOFMT_FILES      := $(shell git ls-files --cached --others --exclude-standard '*.go' | while IFS= read -r file; do [ -f "$$file" ] && printf '%s\n' "$$file"; done)

VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X github.com/jmcampanini/gibson/cmd.Version=$(VERSION)"

.DEFAULT_GOAL := help

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ { printf "  %-16s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

$(WEB_INSTALL_MARK): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	npm ci --prefix $(WEB_DIR)

dev-web: $(WEB_INSTALL_MARK) ## Run the Vite development server.
	npm run dev --prefix $(WEB_DIR)

dev-server: ## Run Gibson with the Vite development proxy.
	go run $(LDFLAGS) $(CMD) serve --dev

web: $(WEB_INSTALL_MARK) ## Build the production web application.
	npm run build --prefix $(WEB_DIR)

build: web ## Build Gibson into ./build/gibson.
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

test: ## Run tests with the race detector.
	go test -race $(PKG)

cli-proof: ## Build and verify the compiled CLI contract.
	@$(MAKE) --no-print-directory build VERSION=cli-proof
	@set -eu; \
	repo_root="$$PWD"; \
	gibson="$$repo_root/$(BINARY)"; \
	sandbox="$$repo_root/.sandbox/cli-proof-$$$$"; \
	trap 'rm -rf "$$sandbox"' EXIT; \
	test -x "$$gibson"; \
	root_help="$$($$gibson --help)"; \
	printf '%s\n' "$$root_help" | grep -Eq '^[[:space:]]+serve[[:space:]]'; \
	printf '%s\n' "$$root_help" | grep -Eq '^[[:space:]]+run[[:space:]]'; \
	version="$$($$gibson --version)"; \
	test "$$version" = "gibson version cli-proof"; \
	serve_help="$$($$gibson serve --help)"; \
	printf '%s\n' "$$serve_help" | grep -Fq -- '--port'; \
	printf '%s\n' "$$serve_help" | grep -Fq -- '--dev'; \
	run_help="$$($$gibson run --help)"; \
	printf '%s\n' "$$run_help" | grep -Fq -- 'run <type> <message>'; \
	if printf '%s\n' "$$run_help" | grep -Fq -- '--checkout'; then \
		echo "run exposed --checkout before Chunk 6" >&2; \
		exit 1; \
	fi; \
	if error_output="$$($$gibson serve unexpected 2>&1)"; then \
		echo "expected positional arguments to fail" >&2; \
		exit 1; \
	else \
		error_rc=$$?; \
	fi; \
	test "$$error_rc" -eq 1; \
	test "$$(printf '%s\n' "$$error_output" | wc -l | tr -d ' ')" -eq 1; \
	case "$$error_output" in 'gibson: error: '*) ;; *) echo "missing process error prefix" >&2; exit 1;; esac; \
	mkdir -p "$$sandbox/main"; \
	go build -o "$$sandbox/pi" ./internal/fakepi; \
	git -C "$$sandbox/main" init -b main -q; \
	printf '[server]\nport = 7311\npi_bin = "%s"\n\n[sessions.quick]\ndescription = "CLI proof"\n' "$$sandbox/pi" > "$$sandbox/main/gibson.toml"; \
	printf '.gibson/\n' > "$$sandbox/main/.gitignore"; \
	git -C "$$sandbox/main" add gibson.toml .gitignore; \
	git -C "$$sandbox/main" -c user.name='Gibson CLI Proof' -c user.email=gibson@example.invalid commit -qm init; \
	cd "$$sandbox/main"; \
	run_stdout="$$($$gibson run quick 'Say hello' 2>"$$sandbox/run.stderr")"; \
	test "$$run_stdout" = 'Hello from fake pi.'; \
	grep -Fq '[session] id=' "$$sandbox/run.stderr"; \
	grep -Fq 'status=stopped' "$$sandbox/run.stderr"; \
	test "$$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')" -eq 1; \
	test "$$(find .gibson/logs -type f -name '*.stderr.log' | wc -l | tr -d ' ')" -eq 1; \
	grep -Fq '"version": 1' .gibson/state.json; \
	grep -Fq '"status": "stopped"' .gibson/state.json; \
	grep -Fq '"pid": 0' .gibson/state.json; \
	test -z "$$(git status --porcelain)"; \
	if unknown_output="$$($$gibson run missing hi 2>&1)"; then \
		echo "expected unknown session type to fail" >&2; \
		exit 1; \
	else \
		unknown_rc=$$?; \
	fi; \
	test "$$unknown_rc" -eq 1; \
	test "$$(printf '%s\n' "$$unknown_output" | wc -l | tr -d ' ')" -eq 1; \
	case "$$unknown_output" in 'gibson: error: '*'configured types: quick'*) ;; *) echo "unknown type error did not list configured types" >&2; exit 1;; esac; \
	FAKEPI_SCENARIO=slow_stream python3 -c 'import os, sys; os.setpgid(0, 0); os.execv(sys.argv[1], sys.argv[1:])' "$$gibson" run quick 'Stream until interrupted' >"$$sandbox/interrupt.stdout" 2>"$$sandbox/interrupt.stderr" & \
	interrupt_pid=$$!; \
	for attempt in $$(seq 1 100); do \
		if test -s "$$sandbox/interrupt.stdout"; then break; fi; \
		kill -0 "$$interrupt_pid" 2>/dev/null || { echo "interrupt proof exited before streaming" >&2; exit 1; }; \
		sleep 0.02; \
	done; \
	test -s "$$sandbox/interrupt.stdout"; \
	python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$$interrupt_pid"; \
	if wait "$$interrupt_pid"; then interrupt_rc=0; else interrupt_rc=$$?; fi; \
	test "$$interrupt_rc" -eq 130; \
	grep -Fq '"stopReason":"aborted"' .gibson/sessions/*.jsonl; \
	python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"; \
	if pgrep -f "$$PWD/.gibson/sessions" >/dev/null; then \
		echo "first interrupt left an orphan" >&2; exit 1; \
	else \
		pgrep_rc=$$?; test "$$pgrep_rc" -eq 1 || { echo "first interrupt orphan check failed" >&2; exit "$$pgrep_rc"; }; \
	fi; \
	printf 'FIRST_INTERRUPT_EXIT=%s\n' "$$interrupt_rc"; \
	printf 'FIRST_INTERRUPT_STDOUT='; tr '\n' ' ' <"$$sandbox/interrupt.stdout"; printf '\n'; \
	printf '%s\n' 'FIRST_INTERRUPT_PROCESS_GROUP=true' 'FIRST_INTERRUPT_ABORTED_ENTRY=true' 'FIRST_INTERRUPT_ORPHANS=0' 'FIRST_INTERRUPT_REGISTRY=stopped,pid=0'; \
	FAKEPI_SCENARIO=slow_stream python3 -c 'import os, sys; os.setpgid(0, 0); os.execv(sys.argv[1], sys.argv[1:])' "$$gibson" run quick 'Force shutdown' >"$$sandbox/force.stdout" 2>"$$sandbox/force.stderr" & \
	force_pid=$$!; \
	for attempt in $$(seq 1 100); do \
		pi_pid="$$(python3 -c "import json; d=json.load(open('.gibson/state.json')); print(next((s['pid'] for s in d['sessions'].values() if s['status']=='live'), ''))")"; \
		if test -n "$$pi_pid" && test -s "$$sandbox/force.stdout"; then break; fi; \
		kill -0 "$$force_pid" 2>/dev/null || { echo "force proof exited before streaming" >&2; exit 1; }; \
		sleep 0.02; \
	done; \
	test -n "$$pi_pid"; \
	kill -STOP "$$pi_pid"; \
	python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$$force_pid"; \
	sleep 0.1; \
	force_started="$$(python3 -c 'import time; print(time.monotonic())')"; \
	python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$$force_pid"; \
	if wait "$$force_pid"; then force_rc=0; else force_rc=$$?; fi; \
	force_finished="$$(python3 -c 'import time; print(time.monotonic())')"; \
	python3 -c 'import sys; elapsed = float(sys.argv[1]) - float(sys.argv[2]); assert elapsed < 5, f"second interrupt took {elapsed:.3f}s"' "$$force_finished" "$$force_started"; \
	test "$$force_rc" -eq 130; \
	if kill -0 "$$pi_pid" 2>/dev/null; then echo "second interrupt left pi alive" >&2; kill -KILL "$$pi_pid"; exit 1; fi; \
	python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"; \
	printf 'SECOND_INTERRUPT_EXIT=%s\n' "$$force_rc"; \
	printf '%s\n' 'SECOND_INTERRUPT_PROMPT=true' 'SECOND_INTERRUPT_ORPHANS=0' 'SECOND_INTERRUPT_REGISTRY=stopped,pid=0'; \
	if FAKEPI_SCENARIO=crash_mid_stream "$$gibson" run quick 'Crash now' >"$$sandbox/crash.stdout" 2>"$$sandbox/crash.stderr"; then \
		echo "expected crash scenario to fail" >&2; \
		exit 1; \
	else \
		crash_rc=$$?; \
	fi; \
	test "$$crash_rc" -eq 1; \
	grep -Fq 'Partial output before crash.' "$$sandbox/crash.stdout"; \
	grep -Fq 'deterministic crash after first delta' "$$sandbox/crash.stderr"; \
	python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"; \
	if pgrep -f "$$PWD/.gibson/sessions" >/dev/null; then \
		echo "crash left an orphan" >&2; exit 1; \
	else \
		pgrep_rc=$$?; test "$$pgrep_rc" -eq 1 || { echo "crash orphan check failed" >&2; exit "$$pgrep_rc"; }; \
	fi; \
	printf 'CRASH_EXIT=%s\n' "$$crash_rc"; \
	printf 'CRASH_STDOUT='; tr '\n' ' ' <"$$sandbox/crash.stdout"; printf '\n'; \
	printf '%s\n' 'CRASH_STDERR_TAIL=preserved' 'CRASH_ORPHANS=0' 'CRASH_REGISTRY=stopped,pid=0'; \
	printf '%s\n' 'GIBSON_CLI_PROOF=PASS'

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

verify: check cli-proof ## Run the complete local and CI verification gate.

clean: ## Remove build artifacts, coverage files, and test cache.
	rm -rf $(BUILD_DIR) $(WEB_DIR)/dist coverage.out coverage.html *.coverprofile
	go clean -testcache
