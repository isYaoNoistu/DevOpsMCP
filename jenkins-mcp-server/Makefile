.PHONY: build test clean

ifeq ($(OS),Windows_NT)
BINARY := jenkins-mcp-server.exe
else
BINARY := jenkins-mcp-server
endif

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f jenkins-mcp-server jenkins-mcp-server.exe
