SHELL := /bin/bash

GO ?= go
GOFMT ?= gofmt
BIN_NAME ?= icloud
CMD_NAME ?= icloud
CMD_PATH ?= ./cmd/$(CMD_NAME)
DIST_DIR ?= dist
BIN_PATH ?= $(DIST_DIR)/$(BIN_NAME)
VERSION ?= $(shell node -p "require('./npm/package.json').version" 2>/dev/null)
LDFLAGS ?= -s -w -X github.com/amxv/icloud-cli/internal/buildinfo.Version=$(if $(VERSION),$(VERSION),dev)

.PHONY: help fmt test vet lint schemas references reference-check check docs-check docs-build build build-all install-local clean release-tag

help:
	@echo "icloud CLI command runner"
	@echo ""
	@echo "Targets:"
	@echo "  make fmt           - format Go files"
	@echo "  make test          - run go test ./..."
	@echo "  make vet           - run go vet ./..."
	@echo "  make lint          - run Node script checks"
	@echo "  make schemas       - regenerate versioned JSON Schemas"
	@echo "  make references    - regenerate schemas, command reference, man pages, and completions"
	@echo "  make reference-check - verify committed generated reference assets"
	@echo "  make check         - fmt + test + vet + lint"
	@echo "  make docs-check    - validate the Astro/ZueDocs site"
	@echo "  make docs-build    - build the Astro/ZueDocs site"
	@echo "  make build         - build local binary to dist/icloud"
	@echo "  make build-all     - build release binaries for 6 target platforms"
	@echo "  make install-local - install CLI to ~/.local/bin/icloud"
	@echo "  make clean         - remove dist artifacts"
	@echo "  make release-tag VERSION=x.y.z - create and push a tag for GitHub Actions"

fmt:
	@$(GOFMT) -w $$(find cmd internal -type f -name '*.go')

test:
	@$(GO) test ./...

vet:
	@$(GO) vet ./...

lint:
	@npm --prefix npm run lint
	@node --test scripts/packaging.test.js scripts/docs-contract.test.js

schemas:
	@$(GO) run ./scripts/schema-gen.go

references: schemas
	@$(GO) run ./scripts/reference-gen.go --version "$(VERSION)"

reference-check:
	@$(GO) run ./scripts/reference-gen.go --version "$(VERSION)" --check
	@$(GO) test -count=1 ./internal/cli -run '^TestGeneratedSchemasMatchCommittedArtifacts$$'

check: fmt reference-check test vet lint

docs-check: reference-check
	@cd docs && bun run check

docs-build: reference-check
	@cd docs && bun run build

build:
	@mkdir -p $(DIST_DIR)
	@$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_PATH) $(CMD_PATH)

build-all:
	@mkdir -p $(DIST_DIR)
	@for target in "darwin amd64" "darwin arm64" "linux amd64" "linux arm64" "windows amd64" "windows arm64"; do \
		set -- $$target; \
		GOOS=$$1; GOARCH=$$2; \
		EXT=""; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		echo "Building $(BIN_NAME) for $$GOOS/$$GOARCH"; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o "$(DIST_DIR)/$(BIN_NAME)_$${GOOS}_$${GOARCH}$${EXT}" $(CMD_PATH); \
	done

install-local: build
	@mkdir -p $$HOME/.local/bin
	@install -m 755 $(BIN_PATH) $$HOME/.local/bin/$(BIN_NAME)
	@echo "Installed $(BIN_NAME) to $$HOME/.local/bin/$(BIN_NAME)"

clean:
	@rm -rf $(DIST_DIR) npm/.publish docs/dist docs/.astro .vercel/output

release-tag:
	@test -n "$(VERSION)" || (echo "Usage: make release-tag VERSION=x.y.z" && exit 1)
	@git tag "v$(VERSION)"
	@git push origin "v$(VERSION)"
