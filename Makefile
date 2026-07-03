.PHONY: build build-go deps dev run setup clean help quickstart test ensure-static test-static lint frontend check-static dev-frontend dev-backend fmt runtime-dirs free-run-port

-include .env
export

FILEHUB_ADDR ?= :17040
FILEHUB_API_KEY_AUTH_ENABLED ?= true
FILEHUB_API_KEYS ?= dev-filehub-key
FILEHUB_PRESIGN_SECRET ?= filehub-presign-secret
FILEHUB_DSN ?= sqlite://.synapse/stack/filehub.db
FILEHUB_STORAGE_BACKEND ?= osfs
FILEHUB_STORAGE_DIR ?= .synapse/stack/filehub-data
FILEHUB_PID_FILE ?= .synapse/stack/filehub.pid
VITE_BASE_PATH ?= ./

# Build frontend (React + Vite)
frontend:
	cd web && pnpm install --prefer-offline && VITE_BASE_PATH="$(VITE_BASE_PATH)" npm run build

check-static: frontend
	@git diff --exit-code -- web/static || (echo "FileHub static assets are stale. Run 'make -C filehub frontend' and commit web/static changes." && exit 1)

# Build the filehub binary (includes embedded frontend)
build: frontend
	go build -o filehub ./cmd/filehub/

# Ensure the embed target exists for Go-only commands in a clean checkout.
ensure-static: test-static

# Build Go only (skip frontend)
build-go: ensure-static
	go build -o filehub ./cmd/filehub/

# Download Go dependencies
deps:
	go mod download

# Create local runtime directories used by the default SQLite + osfs setup.
runtime-dirs:
	@mkdir -p "$$(dirname "$$(printf '%s' '$(FILEHUB_DSN)' | sed 's#^sqlite://##; s#[?].*##')")" "$(FILEHUB_STORAGE_DIR)"

free-run-port:
	@addr="$(FILEHUB_ADDR)"; \
	port="$${addr##*:}"; \
	if ! printf '%s' "$$port" | grep -Eq '^[0-9]+$$'; then \
		echo "Cannot parse listen port from FILEHUB_ADDR=$$addr"; \
		exit 1; \
	fi; \
	if [ -f "$(FILEHUB_PID_FILE)" ]; then \
		old_pid="$$(cat "$(FILEHUB_PID_FILE)" 2>/dev/null || true)"; \
		if [ -n "$$old_pid" ] && kill -0 "$$old_pid" 2>/dev/null; then \
			cmd="$$(ps -p "$$old_pid" -o comm= 2>/dev/null || true)"; \
			if printf '%s' "$$cmd" | grep -Eq '(^|/)filehub$$'; then \
				echo "Stopping previous FileHub process $$old_pid."; \
				kill "$$old_pid" 2>/dev/null || true; \
				sleep 1; \
				if kill -0 "$$old_pid" 2>/dev/null; then kill -9 "$$old_pid" 2>/dev/null || true; fi; \
			fi; \
		fi; \
		rm -f "$(FILEHUB_PID_FILE)"; \
	fi; \
	pids=""; \
	if command -v lsof >/dev/null 2>&1; then \
		pids="$$(lsof -tiTCP:"$$port" -sTCP:LISTEN 2>/dev/null || true)"; \
	elif command -v fuser >/dev/null 2>&1; then \
		pids="$$(fuser "$$port"/tcp 2>/dev/null || true)"; \
	else \
		echo "Skipping port check for $$port: lsof or fuser is required."; \
	fi; \
	if [ -n "$$pids" ]; then \
		for pid in $$pids; do \
			cmd="$$(ps -p "$$pid" -o comm= 2>/dev/null || true)"; \
			if printf '%s' "$$cmd" | grep -Eq '(^|/)filehub$$'; then \
				echo "Stopping FileHub process $$pid on port $$port."; \
				kill "$$pid" 2>/dev/null || true; \
				sleep 1; \
				if kill -0 "$$pid" 2>/dev/null; then kill -9 "$$pid" 2>/dev/null || true; fi; \
			else \
				echo "Port $$port is used by non-FileHub process $$pid ($$cmd). Stop it manually or change FILEHUB_ADDR."; \
				exit 1; \
			fi; \
		done; \
	else \
		echo "Port $$port is available."; \
	fi

# One-click: build + start server (SQLite + local filesystem storage)
quickstart: build runtime-dirs
	@echo "Starting FileHub on $(FILEHUB_ADDR) (SQLite + $(FILEHUB_STORAGE_BACKEND) storage)..."
	FILEHUB_ADDR="$(FILEHUB_ADDR)" \
	FILEHUB_API_KEY_AUTH_ENABLED="$(FILEHUB_API_KEY_AUTH_ENABLED)" \
	FILEHUB_API_KEYS="$(FILEHUB_API_KEYS)" \
	FILEHUB_PRESIGN_SECRET="$(FILEHUB_PRESIGN_SECRET)" \
	FILEHUB_DSN="$(FILEHUB_DSN)" \
	FILEHUB_STORAGE_BACKEND="$(FILEHUB_STORAGE_BACKEND)" \
	FILEHUB_STORAGE_DIR="$(FILEHUB_STORAGE_DIR)" \
	./filehub

# Full setup: download dependencies and build everything
setup: deps build
	@echo "Setup complete."
	@echo "  Start server:  make dev"
	@echo "  Listen addr:   $(FILEHUB_ADDR)"
	@echo "  API key:       $(FILEHUB_API_KEYS)"

# Build frontend + Go binary, then start server
run: build runtime-dirs free-run-port
	@mkdir -p "$$(dirname "$(FILEHUB_PID_FILE)")"; \
	FILEHUB_ADDR="$(FILEHUB_ADDR)" \
	FILEHUB_API_KEY_AUTH_ENABLED="$(FILEHUB_API_KEY_AUTH_ENABLED)" \
	FILEHUB_API_KEYS="$(FILEHUB_API_KEYS)" \
	FILEHUB_PRESIGN_SECRET="$(FILEHUB_PRESIGN_SECRET)" \
	FILEHUB_DSN="$(FILEHUB_DSN)" \
	FILEHUB_STORAGE_BACKEND="$(FILEHUB_STORAGE_BACKEND)" \
	FILEHUB_STORAGE_DIR="$(FILEHUB_STORAGE_DIR)" \
	./filehub & \
	pid="$$!"; \
	echo "$$pid" > "$(FILEHUB_PID_FILE)"; \
	trap 'rm -f "$(FILEHUB_PID_FILE)"' EXIT INT TERM; \
	wait "$$pid"

# Start server in development mode
dev: build runtime-dirs
	FILEHUB_ADDR="$(FILEHUB_ADDR)" \
	FILEHUB_API_KEY_AUTH_ENABLED="$(FILEHUB_API_KEY_AUTH_ENABLED)" \
	FILEHUB_API_KEYS="$(FILEHUB_API_KEYS)" \
	FILEHUB_PRESIGN_SECRET="$(FILEHUB_PRESIGN_SECRET)" \
	FILEHUB_DSN="$(FILEHUB_DSN)" \
	FILEHUB_STORAGE_BACKEND="$(FILEHUB_STORAGE_BACKEND)" \
	FILEHUB_STORAGE_DIR="$(FILEHUB_STORAGE_DIR)" \
	./filehub

# Start frontend dev server (hot reload, proxies API to Go backend)
dev-frontend:
	cd web && npm run dev

# Start Go backend only (for development with frontend dev server)
dev-backend: build-go runtime-dirs
	FILEHUB_ADDR="$(FILEHUB_ADDR)" \
	FILEHUB_API_KEY_AUTH_ENABLED="$(FILEHUB_API_KEY_AUTH_ENABLED)" \
	FILEHUB_API_KEYS="$(FILEHUB_API_KEYS)" \
	FILEHUB_PRESIGN_SECRET="$(FILEHUB_PRESIGN_SECRET)" \
	FILEHUB_DSN="$(FILEHUB_DSN)" \
	FILEHUB_STORAGE_BACKEND="$(FILEHUB_STORAGE_BACKEND)" \
	FILEHUB_STORAGE_DIR="$(FILEHUB_STORAGE_DIR)" \
	./filehub

# Run tests
#
# 排除 web/node_modules,避免 npm 依赖中可能夹带的 Go 文件被 `go test ./...`
# 当成项目包扫描。
test: test-static
	go test $$(go list ./... | grep -v /web/node_modules/)

# Create the smallest embedded frontend tree needed by Go-only commands when
# the real Vite build has not been generated yet. The files live under ignored
# web/static/ so production builds can freely replace them.
test-static:
	@mkdir -p web/static/assets
	@printf '<!doctype html><title>FileHub test shell</title><div id="root"></div>\n' > web/static/index.html
	@printf '' > web/static/assets/.gitkeep

# Lint
#
# golangci-lint run 期待文件/目录路径(不接受全限定 import path),
# 所以用 `go list -f '{{.Dir}}'` 把包路径转成绝对目录,再过滤掉
# web/node_modules(同 test 目标的理由)。
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not installed: https://golangci-lint.run/usage/install/"; exit 1; \
	}
	golangci-lint run $$(go list -f '{{.Dir}}' ./... | grep -v /web/node_modules/)

# Check Go formatting
fmt:
	@test -z "$$(git ls-files '*.go' | xargs gofmt -l)" || (echo "gofmt needed on:" && git ls-files '*.go' | xargs gofmt -l && exit 1)

# Remove local build and runtime artifacts
clean:
	rm -f filehub
	rm -rf .synapse/
	rm -rf web/static/
	@echo "Cleaned."

# Show help
help:
	@echo "FileHub Development"
	@echo ""
	@echo "Quick start (SQLite + local filesystem storage):"
	@echo "  make quickstart"
	@echo ""
	@echo "Frontend development (hot reload):"
	@echo "  make dev-backend                       # Start Go backend"
	@echo "  make dev-frontend                      # Start Vite dev server (in another terminal)"
	@echo ""
	@echo "Targets:"
	@echo "  quickstart    - One-click: build + start server (SQLite + local storage)"
	@echo "  run           - Build frontend + Go binary, then start server"
	@echo "  build         - Build frontend + Go binary"
	@echo "  build-go      - Build Go binary only (skip frontend)"
	@echo "  frontend      - Build frontend only"
	@echo "  deps          - Download Go dependencies"
	@echo "  runtime-dirs  - Create local runtime directories"
	@echo "  setup         - Download dependencies + build"
	@echo "  dev           - Build all + start server"
	@echo "  dev-frontend  - Start Vite dev server (hot reload)"
	@echo "  dev-backend   - Build Go + start server"
	@echo "  test          - Run tests"
	@echo "  lint          - Run golangci-lint"
	@echo "  fmt           - Check Go formatting"
	@echo "  clean         - Remove local build and runtime artifacts"
	@echo "  help          - Show this help"
