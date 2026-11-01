# Orbit — build, test and lint entry points.
#
# The workspace ships its own Go toolchain; source it first:
#   source ../.toolchains/env.sh

# Read from go.mod instead of hard-coding it: the -X targets must match the
# module path, and a repo-wide module rename must not silently drop version
# stamping (an -X on a dead symbol is ignored without an error).
MODULE  := $(shell sed -n 's/^module[[:space:]]*//p' go.mod 2>/dev/null)
# Nearest tag, or a neutral placeholder. A commit hash is never reported as a
# version — the hash already travels in COMMIT. Override with "make VERSION=v1.2.3";
# release builds run in CI where the tag is present, so they never see the fallback.
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo v0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) \
           -X $(MODULE)/internal/version.Commit=$(COMMIT) \
           -X $(MODULE)/internal/version.Date=$(DATE)

.PHONY: all build dist release test vet fmt fmt-check check tidy lint verify-api install clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/orbit ./cmd/orbit

# Cross-compiled release packages for every supported platform (9 targets:
# linux amd64/arm64/armv7/386, darwin amd64/arm64, windows amd64/arm64,
# freebsd amd64). The matrix, packaging and checksums live in
# scripts/build-dist.sh so local builds and CI cannot drift.
# Override the version with "make dist VERSION=v1.2.3".
dist:
	@VERSION="$(VERSION)" bash scripts/build-dist.sh --all

# Historical name: "make release" is the same as "make dist".
release: dist

test:
	go test ./...

vet:
	go vet ./...

# Rewrite formatting in place. To only verify, use "make fmt-check" or "make check".
fmt:
	gofmt -l -w .

# Fail without touching any file: the gate CI enforces on every push.
fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt is required on:"; echo "$$files"; \
		echo "run 'make fmt' to fix them"; \
		exit 1; \
	fi

# Exactly what CI runs: formatting, vet, tests. Offline by design.
check: fmt-check vet test

tidy:
	go mod tidy

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipped"

# M1: probe the live API for the facts marked "未验证" in docs/TENCENT_API_NOTES.md.
verify-api:
	go run ./cmd/orbit doctor --demo

install: build
	install -m 0755 bin/orbit /usr/local/bin/orbit

clean:
	rm -rf bin dist
