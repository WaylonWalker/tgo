default: help

help:
    @just --list

fmt:
    @go fmt ./...

lint:
    @if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run ./...; \
    else \
        echo "golangci-lint not found; install it to enable lint checks."; \
        exit 0; \
    fi

test:
    @go test ./...

build:
    @mkdir -p bin
    @go build -ldflags "-s -w" -o bin/tgo ./cmd/tgo

install:
    @go install ./cmd/tgo

run:
    @go run ./cmd/tgo

tidy:
    @go mod tidy

ci: fmt lint test
