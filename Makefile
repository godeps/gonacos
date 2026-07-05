.PHONY: build build-go deps dev run stop setup quickstart clean help test fmt vet contract-generate contract-verify frontend web build-web runtime-dirs free-run-port

-include .env
export

# gonacos defaults — override via .env, shell env, or `make run GONACOS_ADDR=:9000`.
GONACOS_ADDR ?= :8848
GONACOS_DATA_DIR ?= .gonacos/data
GONACOS_PID_FILE ?= .gonacos/gonacos.pid
GONACOS_AUTH_SECRET ?= dev-gonacos-secret
VITE_BASE_PATH ?= ./

# Build the React SPA (Vite) into pkg/web/console-ui/dist/ for go:embed.
# Skipped when npm is not installed — embed.go falls back to an empty
# dist tree, and the server serves the legacy console.html at
# /v3/console/ui/legacy. Operators who want the full React UI must
# have Node.js installed.
frontend:
	@if command -v npm >/dev/null 2>&1; then \
		bash pkg/web/build-web.sh; \
	else \
		echo "npm not found; skipping React SPA build (legacy console.html will be served)."; \
	fi

# Alias matching the saker-stack sibling convention (chathub, filehub, aihub).
web: frontend

# Build gonacos + gonacos-contract binaries with the embedded frontend.
build: frontend
	GOWORK=off go build -o gonacos ./cmd/gonacos
	GOWORK=off go build -o gonacos-contract ./cmd/gonacos-contract

# Build Go binaries only (skip frontend). Use when web assets are already
# up to date or when npm is unavailable.
build-go:
	GOWORK=off go build -o gonacos ./cmd/gonacos
	GOWORK=off go build -o gonacos-contract ./cmd/gonacos-contract

# Full build: frontend + binaries. Alias matching sibling convention.
build-web: build

# Download Go dependencies.
deps:
	GOWORK=off go mod download

# Create local runtime directories used by the default embedded-Redis
# setup: data dir for snapshots/dumps, parent of PID file.
runtime-dirs:
	@mkdir -p "$(GONACOS_DATA_DIR)" "$$(dirname "$(GONACOS_PID_FILE)")"

# Verify the configured listen port is free before starting. Prevents
# the "address already in use" surprise midway through a dev session.
# Uses ss(8) when available; no-op otherwise.
free-run-port:
	@addr="$(GONACOS_ADDR)"; \
	port="$${addr##*:}"; \
	if ! printf '%s' "$$port" | grep -Eq '^[0-9]+$$'; then \
		echo "Cannot parse listen port from GONACOS_ADDR=$$addr"; \
		exit 1; \
	fi; \
	if command -v ss >/dev/null 2>&1; then \
		if ss -ltn "sport = :$$port" 2>/dev/null | grep -q ":$$port"; then \
			echo "Port $$port is already in use (GONACOS_ADDR=$(GONACOS_ADDR))"; \
			exit 1; \
		fi; \
	fi

# One-time setup: deps + build.
setup: deps build
	@echo "Setup complete."
	@echo "  Start server:  make run     (background, PID file)"
	@echo "  Or:            make dev     (foreground)"
	@echo "  Listen addr:   $(GONACOS_ADDR)"
	@echo "  Data dir:      $(GONACOS_DATA_DIR)"
	@echo "  Console UI:    http://localhost:8848/v3/console/ui"
	@echo "  Default login: admin / nacos"

# Start server in the background with a PID file. Ctrl-C (SIGINT) is
# delivered to the whole process group, so gonacos shuts down via its
# signal.NotifyContext handler; the trap cleans up the PID file.
run: build runtime-dirs free-run-port
	@mkdir -p "$$(dirname "$(GONACOS_PID_FILE)")"; \
	PORT="$$(printf '%s' '$(GONACOS_ADDR)' | sed 's/^://')"; \
	GONACOS_DATA_DIR="$(GONACOS_DATA_DIR)" \
	GONACOS_AUTH_SECRET="$(GONACOS_AUTH_SECRET)" \
	./gonacos serve "$(GONACOS_ADDR)" & \
	pid="$$!"; \
	echo "$$pid" > "$(GONACOS_PID_FILE)"; \
	trap 'kill "$$pid" 2>/dev/null; rm -f "$(GONACOS_PID_FILE)"' EXIT INT TERM; \
	echo "gonacos started (pid $$pid, listening on $(GONACOS_ADDR))"; \
	echo "Console UI: http://localhost:$$PORT/v3/console/ui"; \
	echo "Default login: admin / nacos"; \
	echo "Stop with: make stop (or Ctrl-C)"; \
	wait "$$pid"

# Start server in the foreground (Ctrl-C to stop).
dev: build runtime-dirs
	@PORT="$$(printf '%s' '$(GONACOS_ADDR)' | sed 's/^://')"; \
	GONACOS_DATA_DIR="$(GONACOS_DATA_DIR)" \
	GONACOS_AUTH_SECRET="$(GONACOS_AUTH_SECRET)" \
	./gonacos serve "$(GONACOS_ADDR)"; \
	echo "Console UI was at http://localhost:$$PORT/v3/console/ui"

# Quickstart: build + start in foreground.
quickstart: dev

# Stop a background server started by `make run`.
stop:
	@if [ -f "$(GONACOS_PID_FILE)" ]; then \
		pid=$$(cat "$(GONACOS_PID_FILE)"); \
		if kill -0 "$$pid" 2>/dev/null; then \
			kill "$$pid"; \
			echo "gonacos (pid $$pid) stopped"; \
		else \
			echo "PID $$pid from $(GONACOS_PID_FILE) is not running (stale PID file)"; \
		fi; \
		rm -f "$(GONACOS_PID_FILE)"; \
	else \
		echo "No PID file at $(GONACOS_PID_FILE) — server not started via 'make run'?"; \
	fi

test:
	GOWORK=off go test ./...

fmt:
	gofmt -s -w .

vet:
	GOWORK=off go vet ./...

contract-generate:
	GOWORK=off go run ./cmd/gonacos-contract -write

contract-verify:
	GOWORK=off go run ./cmd/gonacos-contract -verify

clean:
	rm -f gonacos gonacos-contract
	rm -rf pkg/web/console-ui/dist

help:
	@echo "gonacos — Nacos v3 compatible server"
	@echo ""
	@echo "Quick start (embedded Redis, local data):"
	@echo "  make setup       # one-time: deps + build"
	@echo "  make run         # start in background (PID file)"
	@echo "  make dev         # start in foreground"
	@echo "  make stop        # stop background server"
	@echo ""
	@echo "Listen addr:    $(GONACOS_ADDR)"
	@echo "Data dir:       $(GONACOS_DATA_DIR)"
	@echo "Console UI:     http://localhost:8848/v3/console/ui"
	@echo "Default login:  admin / nacos"
	@echo ""
	@echo "Build:"
	@echo "  make build       # build frontend + binaries"
	@echo "  make build-go    # skip frontend, build binaries only"
	@echo "  make frontend    # build React SPA only"
	@echo "  make clean       # remove binaries and web build artifacts"
	@echo ""
	@echo "Development:"
	@echo "  make test        # go test ./..."
	@echo "  make fmt         # gofmt -s -w ."
	@echo "  make vet         # go vet ./..."
	@echo ""
	@echo "Contract:"
	@echo "  make contract-generate"
	@echo "  make contract-verify"
