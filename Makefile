# All targets run inside the dev container. Nothing is built or installed on the host.
SHELL := /bin/sh
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

COMPOSE := docker compose
EXEC    := $(COMPOSE) exec -T dev
# A one-off container with the staging token file mounted (compose.live.yaml).
LIVE    := $(COMPOSE) -f compose.yaml -f compose.live.yaml run --rm
PKG     := github.com/s8a-uk/modrinth-cli
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/buildinfo.Version=$(VERSION)
GOBUILD := go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)"
ARGS    ?=

.PHONY: up down rebuild sh build run run-staging test test-update test-live fmt fmt-check vet lint \
        deps-check check dist linux windows darwin package smoke drift-smoke size clean

up:
	@$(COMPOSE) up -d --build --quiet-build --quiet-pull dev

down:
	$(COMPOSE) down

rebuild:
	$(COMPOSE) build --no-cache dev
	$(COMPOSE) up -d dev

sh: up
	$(COMPOSE) exec dev bash

build: up
	$(EXEC) $(GOBUILD) -o bin/mdr ./cmd/mdr

run: up
	$(EXEC) go run ./cmd/mdr $(ARGS)

test: up
	$(EXEC) go test ./... $(ARGS)

# Only test binaries that import testutil define -update, so pass it to those packages alone.
test-update: up
	$(EXEC) sh -c 'go test $$(go list -f "{{.ImportPath}} {{.TestImports}} {{.XTestImports}}" ./... | grep /internal/testutil | cut -d" " -f1) -update'

# The staging token is read from MODRINTH_STAGING_TOKEN_PATH
# (default ~/.config/modrinth-cli/staging-token) and mounted read-only only for
# these two targets. The live tests run only with MODRINTH_LIVE_TEST=1, and the
# version lifecycle needs MODRINTH_LIVE_PROJECT (a staging project id); compose.yaml
# passes both through from the environment of make:
#   MODRINTH_LIVE_TEST=1 MODRINTH_LIVE_PROJECT=<id> make test-live
test-live:
	$(LIVE) -T dev go test -tags live -count=1 -v ./internal/livetest/...

run-staging:
	$(LIVE) dev go run ./cmd/mdr --staging $(ARGS)

fmt: up
	$(EXEC) gofmt -w .

fmt-check: up
	@$(EXEC) sh -c 'out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi'

vet: up
	$(EXEC) go vet ./...

# The live tests (build tag live) are type-checked here but only run by make test-live.
lint: vet
	$(EXEC) staticcheck ./...
	$(EXEC) go vet -tags live ./internal/livetest/...

deps-check: up
	$(EXEC) sh scripts/deps-check.sh

check: fmt-check lint test deps-check

dist: linux windows darwin

linux: up
	$(EXEC) env GOOS=linux GOARCH=amd64 $(GOBUILD) -o dist/mdr_linux_amd64 ./cmd/mdr
	$(EXEC) env GOOS=linux GOARCH=arm64 $(GOBUILD) -o dist/mdr_linux_arm64 ./cmd/mdr

windows: up
	$(EXEC) env GOOS=windows GOARCH=amd64 $(GOBUILD) -o dist/mdr_windows_amd64.exe ./cmd/mdr

darwin: up
	$(EXEC) env GOOS=darwin GOARCH=arm64 $(GOBUILD) -o dist/mdr_darwin_arm64 ./cmd/mdr

# Release archives and SHA256SUMS in dist/release/ from the binaries in dist/.
# VERSION must be the release tag: make dist package VERSION=v0.1.0
# The archive time stamps are the last commit's, so the archives are reproducible.
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
package: up
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "make package: set VERSION to the tag, e.g. VERSION=v0.1.0" >&2; exit 2;; esac
	$(EXEC) env SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) sh scripts/package.sh $(patsubst v%,%,$(VERSION))

smoke: linux
	$(EXEC) ./dist/mdr_linux_amd64 --version
	$(EXEC) sh -c './dist/mdr_linux_amd64 schema > /dev/null'

# A few anonymous GET requests to api.modrinth.com (production); see .github/workflows/drift.yml.
drift-smoke: build
	$(EXEC) sh scripts/drift-smoke.sh ./bin/mdr

size: up
	@$(EXEC) sh -c 'for f in dist/*; do printf "%10d bytes  %s\n" "$$(wc -c < "$$f")" "$$f"; done'

clean: up
	$(EXEC) rm -rf bin dist
