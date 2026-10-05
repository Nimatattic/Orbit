# Orbit — build, test and lint entry points.
#
# The workspace ships its own Go toolchain; source it first:
#   source ../.toolchains/env.sh

MODULE  := github.com/Crequency/orbit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) \
           -X $(MODULE)/internal/version.Commit=$(COMMIT) \
           -X $(MODULE)/internal/version.Date=$(DATE)

.PHONY: all build test vet fmt lint tidy verify-api clean install

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/orbit ./cmd/orbit

# Static binaries for the machines Orbit actually runs on.
release:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orbit-linux-amd64  ./cmd/orbit
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orbit-linux-arm64  ./cmd/orbit
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orbit-darwin-arm64 ./cmd/orbit

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

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
