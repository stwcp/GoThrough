# Library project: make check (default). Run the sample proxy with make example.
.PHONY: all init mod-sync git-hooks fmt check test test-coverage example

GO ?= go
PKG := ./...
COV_DIR := internal/test/coverage
REPO_ROOT := $(abspath $(CURDIR))
TOOLS_DIR := $(REPO_ROOT)/tools

DLV       := $(TOOLS_DIR)/dlv
GOLANGCI  := $(TOOLS_DIR)/golangci-lint
GOPLS     := $(TOOLS_DIR)/gopls
GOIMPORTS := $(TOOLS_DIR)/goimports

DLV_VER         ?= v1.26.3
GOPLS_VER       ?= v0.22.0
GOLANGCI_VER    ?= v2.12.0
GOIMPORTS_VER   ?= v0.44.0
GOVULNCHECK_VER ?= v1.3.0

all: check

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

fmt:
	$(GOLANGCI) fmt

test:
	$(GO) test -race -count=1 $(PKG)

check:
	$(GOLANGCI) run
	$(GO) test -race -count=1 $(PKG)
	$(GO) build $(PKG)

test-coverage:
	@mkdir -p $(COV_DIR)
	$(GO) test -race -count=1 -coverprofile=$(COV_DIR)/coverage.out -covermode=atomic $(PKG)
	@echo ""
	@echo "== coverage by function ($(COV_DIR)/coverage.txt); line report: $(COV_DIR)/coverage.html =="
	@$(GO) tool cover -func=$(COV_DIR)/coverage.out | tee $(COV_DIR)/coverage.txt
	@$(GO) tool cover -html=$(COV_DIR)/coverage.out -o $(COV_DIR)/coverage.html
	@echo "Open $(COV_DIR)/coverage.html in a browser for line-level coverage."

example:
	$(GO) run ./examples/reference
