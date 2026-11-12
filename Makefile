GO ?= go

.DEFAULT_GOAL := help

.PHONY: help build test lint format generate-check release release-list test-release

VERSION ?=
SKIP_TESTS ?=
DRY_RUN ?=
AUTOPUSH ?= 0

help:
	@printf '%s\n' 'make test            Run all bundler tests' 'make lint            Check formatting and run go vet' 'make build           Compile the command' 'make generate-check  Verify generated-file cleanliness (no generated sources)' 'make release VERSION=patch  Prepare a release tag' 'make release-list    List release tags'

build:
	$(GO) build ./...

test:
	$(GO) test ./...

lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	$(GO) vet ./...

format:
	@gofmt -w *.go

generate-check:
	@git diff --exit-code -- .

release-list:
	@git tag -l 'v*' --sort=-version:refname

release:
	@[ -n "$(VERSION)" ] || { echo "VERSION required: patch, minor, major, or X.Y.Z" >&2; exit 2; }
	@python3 scripts/release.py --version "$(VERSION)" $(if $(strip $(DRY_RUN)),--dry-run) $(if $(strip $(SKIP_TESTS)),--skip-tests) $(if $(filter 1,$(AUTOPUSH)),--autopush)

test-release:
	@bash scripts/test-release.sh
