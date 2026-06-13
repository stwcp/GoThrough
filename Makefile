# Unit: make test / make check (no Docker, no integration build tag).
# Integration: make test-integration (-tags=integration; Docker recommended — Postgres tests skip if unavailable).
.PHONY: all format check test test-integration test-coverage build run tidy clean init mod-sync git-hooks gen schema-gen sqlc-gen db-migrate

GO ?= go
PKG := ./...
COV_DIR := internal/test/coverage
REPO_ROOT := $(abspath $(CURDIR))
TOOLS_DIR := $(REPO_ROOT)/tools

SQLC      := $(TOOLS_DIR)/sqlc
DLV       := $(TOOLS_DIR)/dlv
GOLANGCI  := $(TOOLS_DIR)/golangci-lint
GOPLS     := $(TOOLS_DIR)/gopls
GOIMPORTS := $(TOOLS_DIR)/goimports

DLV_VER       ?= v1.26.3
GOPLS_VER     ?= v0.22.0
GOLANGCI_VER  ?= v2.12.0
GOIMPORTS_VER ?= v0.44.0
GOVULNCHECK_VER ?= v1.3.0

all: format

mod-sync:
	$(GO) mod tidy
	$(GO) mod download

init: mod-sync
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/go-delve/delve/cmd/dlv@$(DLV_VER)
	GOBIN=$(TOOLS_DIR) $(GO) install golang.org/x/tools/gopls@$(GOPLS_VER)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VER)
	GOBIN=$(TOOLS_DIR) $(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VER)
	GOBIN=$(TOOLS_DIR) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER)

git-hooks:
	@git config core.hooksPath .githooks

build:
	$(GO) build -o ./bin/api ./cmd/server

format:
	$(GOLANGCI) fmt
	$(MAKE) check

test:
	$(GO) test -race -count=1 $(PKG)

test-integration:
	$(GO) test -race -count=1 -tags=integration $(PKG)

test-coverage:
	@mkdir -p $(COV_DIR)
	$(GO) test -race -count=1 -coverprofile=$(COV_DIR)/coverage.out -covermode=atomic $(PKG)
	@echo ""
	@echo "== coverage by function ($(COV_DIR)/coverage.txt); line report: $(COV_DIR)/coverage.html =="
	@$(GO) tool cover -func=$(COV_DIR)/coverage.out | tee $(COV_DIR)/coverage.txt
	@$(GO) tool cover -html=$(COV_DIR)/coverage.out -o $(COV_DIR)/coverage.html
	@echo "Open $(COV_DIR)/coverage.html in a browser for line-level coverage."

check:
	$(GOLANGCI) run
	$(GO) test -race -count=1 $(PKG)
	$(GO) build $(PKG)

run:
	if [ -f .env ]; then set -a && . ./.env && set +a; fi; $(GO) run ./cmd/server

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean
	rm -rf ./bin $(COV_DIR) coverage.out coverage.txt coverage.html
