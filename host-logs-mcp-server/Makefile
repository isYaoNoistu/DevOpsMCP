.PHONY: build test clean

ifeq ($(OS),Windows_NT)
BINARY := host-logs-mcp-server.exe
else
BINARY := host-logs-mcp-server
endif

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f host-logs-mcp-server host-logs-mcp-server.exe
