.PHONY: build clean test

ROOT := $(shell pwd -P)
GIT_COMMIT := $(shell git --work-tree ${ROOT} rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git --work-tree ${ROOT} describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
BUILD_DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -w -s -X main.version=$(GIT_TAG) -X main.commit=$(GIT_COMMIT) -X main.date=$(BUILD_DATE)

ifeq ($(OS),Windows_NT)
BINARY_NAME := n9e-mcp-server.exe
else
BINARY_NAME := n9e-mcp-server
endif

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/n9e-mcp-server/

clean:
	rm -f n9e-mcp-server n9e-mcp-server.exe

test:
	go test ./...
