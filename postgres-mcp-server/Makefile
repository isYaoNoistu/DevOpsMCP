.PHONY: build test clean

ifeq ($(OS),Windows_NT)
BINARY := postgres-mcp-server.exe
else
BINARY := postgres-mcp-server
endif

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f postgres-mcp-server postgres-mcp-server.exe postgres-mcp-server.exe~
