.PHONY: build build-go deps dev run setup clean help quickstart test ensure-static test-static lint frontend dev-frontend dev-backend fmt runtime-dirs free-run-port

-include .env
export

ASSETHUB_ADDR ?= :17040
ASSETHUB_API_KEYS ?= dev-assethub-key
ASSETHUB_PRESIGN_SECRET ?= assethub-presign-secret
ASSETHUB_DSN ?= sqlite://.synapse/stack/assethub.db
ASSETHUB_STORAGE_BACKEND ?= osfs
ASSETHUB_STORAGE_DIR ?= .synapse/stack/assethub-data

# Build frontend (React + Vite)
frontend:
	cd web && npm install && npm run build

# Build the assethub binary (includes embedded frontend)
build: frontend
	go build -o assethub ./cmd/assethub/

# Ensure the embed target exists for Go-only commands in a clean checkout.
ensure-static: test-static

# Build Go only (skip frontend)
build-go: ensure-static
	go build -o assethub ./cmd/assethub/

# Download Go dependencies
deps:
	go mod download

# Create local runtime directories used by the default SQLite + osfs setup.
runtime-dirs:
	@mkdir -p "$$(dirname "$$(printf '%s' '$(ASSETHUB_DSN)' | sed 's#^sqlite://##; s#[?].*##')")" "$(ASSETHUB_STORAGE_DIR)"

free-run-port:
	@addr="$(ASSETHUB_ADDR)"; \
	port="$${addr##*:}"; \
	if ! printf '%s' "$$port" | grep -Eq '^[0-9]+$$'; then \
		echo "Cannot parse listen port from ASSETHUB_ADDR=$$addr"; \
		exit 1; \
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
		echo "Port $$port is in use; stopping process(es): $$pids"; \
		kill $$pids 2>/dev/null || true; \
		sleep 1; \
		for pid in $$pids; do \
			if kill -0 "$$pid" 2>/dev/null; then \
				kill -9 "$$pid" 2>/dev/null || true; \
			fi; \
		done; \
	else \
		echo "Port $$port is available."; \
	fi

# One-click: build + start server (SQLite + local filesystem storage)
quickstart: build runtime-dirs
	@echo "Starting AssetHub on $(ASSETHUB_ADDR) (SQLite + $(ASSETHUB_STORAGE_BACKEND) storage)..."
	ASSETHUB_ADDR="$(ASSETHUB_ADDR)" \
	ASSETHUB_API_KEYS="$(ASSETHUB_API_KEYS)" \
	ASSETHUB_PRESIGN_SECRET="$(ASSETHUB_PRESIGN_SECRET)" \
	ASSETHUB_DSN="$(ASSETHUB_DSN)" \
	ASSETHUB_STORAGE_BACKEND="$(ASSETHUB_STORAGE_BACKEND)" \
	ASSETHUB_STORAGE_DIR="$(ASSETHUB_STORAGE_DIR)" \
	./assethub

# Full setup: download dependencies and build everything
setup: deps build
	@echo "Setup complete."
	@echo "  Start server:  make dev"
	@echo "  Listen addr:   $(ASSETHUB_ADDR)"
	@echo "  API key:       $(ASSETHUB_API_KEYS)"

# Build frontend + Go binary, then start server
run: build runtime-dirs free-run-port
	ASSETHUB_ADDR="$(ASSETHUB_ADDR)" \
	ASSETHUB_API_KEYS="$(ASSETHUB_API_KEYS)" \
	ASSETHUB_PRESIGN_SECRET="$(ASSETHUB_PRESIGN_SECRET)" \
	ASSETHUB_DSN="$(ASSETHUB_DSN)" \
	ASSETHUB_STORAGE_BACKEND="$(ASSETHUB_STORAGE_BACKEND)" \
	ASSETHUB_STORAGE_DIR="$(ASSETHUB_STORAGE_DIR)" \
	./assethub

# Start server in development mode
dev: build runtime-dirs
	ASSETHUB_ADDR="$(ASSETHUB_ADDR)" \
	ASSETHUB_API_KEYS="$(ASSETHUB_API_KEYS)" \
	ASSETHUB_PRESIGN_SECRET="$(ASSETHUB_PRESIGN_SECRET)" \
	ASSETHUB_DSN="$(ASSETHUB_DSN)" \
	ASSETHUB_STORAGE_BACKEND="$(ASSETHUB_STORAGE_BACKEND)" \
	ASSETHUB_STORAGE_DIR="$(ASSETHUB_STORAGE_DIR)" \
	./assethub

# Start frontend dev server (hot reload, proxies API to Go backend)
dev-frontend:
	cd web && npm run dev

# Start Go backend only (for development with frontend dev server)
dev-backend: build-go runtime-dirs
	ASSETHUB_ADDR="$(ASSETHUB_ADDR)" \
	ASSETHUB_API_KEYS="$(ASSETHUB_API_KEYS)" \
	ASSETHUB_PRESIGN_SECRET="$(ASSETHUB_PRESIGN_SECRET)" \
	ASSETHUB_DSN="$(ASSETHUB_DSN)" \
	ASSETHUB_STORAGE_BACKEND="$(ASSETHUB_STORAGE_BACKEND)" \
	ASSETHUB_STORAGE_DIR="$(ASSETHUB_STORAGE_DIR)" \
	./assethub

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
	@printf '<!doctype html><title>AssetHub test shell</title><div id="root"></div>\n' > web/static/index.html
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
	rm -f assethub
	rm -rf .synapse/
	rm -rf web/static/
	@echo "Cleaned."

# Show help
help:
	@echo "AssetHub Development"
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
