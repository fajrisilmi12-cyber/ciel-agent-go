# Makefile for Ciel Agent Go
# Provides convenience targets for building, testing, and releasing.

APP_NAME  := ciel
VERSION   ?= dev
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)
DIST      := dist

# Default target
.PHONY: all
all: build

# ─── Development ───────────────────────────────────────────────

.PHONY: build
build: ## Build for current platform
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(APP_NAME)$(shell [ "$$(go env GOOS)" = "windows" ] && echo ".exe") ./cmd/hermes

.PHONY: dev
dev: ## Build without optimizations (faster compile)
	go build -gcflags="all=-N -l" -o $(APP_NAME)$(shell [ "$$(go env GOOS)" = "windows" ] && echo ".exe") ./cmd/hermes

.PHONY: run
run: build ## Build and run
	./$(APP_NAME)

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: test-v
test-v: ## Run tests verbose
	go test -v ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DIST) $(APP_NAME) $(APP_NAME).exe

# ─── Cross-Platform Builds ─────────────────────────────────────

.PHONY: build-all
build-all: build-linux build-darwin build-windows ## Build for all platforms

.PHONY: build-linux
build-linux: ## Build for Linux (amd64 + arm64)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-linux-amd64 ./cmd/hermes
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-linux-arm64 ./cmd/hermes
	@echo "✅ Linux builds complete"

.PHONY: build-darwin
build-darwin: ## Build for macOS (amd64 + arm64)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-darwin-amd64 ./cmd/hermes
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-darwin-arm64 ./cmd/hermes
	@echo "✅ macOS builds complete"

.PHONY: build-windows
build-windows: ## Build for Windows (amd64 + arm64)
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-windows-amd64.exe ./cmd/hermes
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP_NAME)-windows-arm64.exe ./cmd/hermes
	@echo "✅ Windows builds complete"

# ─── Release ───────────────────────────────────────────────────

.PHONY: release
release: VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.0.0-dev")
release: clean build-all ## Full release build (all platforms, versioned)
	@echo ""
	@echo "🎉 Release $(VERSION) built!"
	@ls -lh $(DIST)/
	@echo ""
	@echo "SHA256 checksums:"
	@cd $(DIST) && sha256sum * 2>/dev/null || shasum -a 256 *

# ─── Help ──────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
