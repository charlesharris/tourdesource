BINARY := tds
PKG := github.com/charlesharris/tourdesource
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X $(PKG)/internal/cli.version=$(VERSION)

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the tds binary into ./bin
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

.PHONY: install
install: ## Install tds into GOBIN
	go install -ldflags "$(LDFLAGS)" .

# The tree-sitter fallback provider is a separate CGO module, deliberately kept
# out of the CGO-free core (docs/spikes/tds-4-static-build.md). It therefore
# builds natively per-OS rather than cross-compiling with the core.
.PHONY: provider-treesitter
provider-treesitter: ## Build the tree-sitter fallback provider into ./bin
	@mkdir -p bin
	cd providers/treesitter && CGO_ENABLED=1 go build -o ../../bin/tds-provider-treesitter .

.PHONY: providers
providers: provider-treesitter ## Build all bundled provider binaries

# The viewer is a Preact app compiled to internal/viewer/assets/{viewer.js,css},
# which are COMMITTED and go:embed'd. `make build` must never need Node — this
# target is for changing the viewer, not for building tds.
.PHONY: viewer
viewer: ## Rebuild the viewer frontend into internal/viewer/assets
	cd viewer && npm install --silent && npm run build

.PHONY: viewer-dev
viewer-dev: ## Run the viewer dev server against a built tour bundle
	cd viewer && npm run dev

.PHONY: viewer-check
viewer-check: ## Typecheck the viewer sources
	cd viewer && npm run check

.PHONY: run
run: ## Run tds (pass args with ARGS="...")
	go run . $(ARGS)

.PHONY: test
test: ## Run tests (core + provider modules)
	go test ./...
	cd providers/treesitter && go test ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Check formatting (fails if any file needs gofmt)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: fmt vet ## Formatting + vet

.PHONY: check
check: lint test ## Everything CI runs

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
