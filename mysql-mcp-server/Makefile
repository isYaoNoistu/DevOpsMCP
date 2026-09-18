.PHONY: build test clean

ifeq ($(OS),Windows_NT)
BINARY := mysql-mcp-server.exe
else
BINARY := mysql-mcp-server
endif

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f mysql-mcp-server mysql-mcp-server.exe mysql-mcp-server.exe~
