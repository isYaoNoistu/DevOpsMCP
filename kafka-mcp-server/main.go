// Kafka read-only diagnostics over stdio. No producer, commit or admin writes.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"kafka-mcp-server/internal/broker"
	"kafka-mcp-server/internal/targets"
	"kafka-mcp-server/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			fmt.Println("kafka-mcp-server " + version)
			return nil
		}
		return fmt.Errorf("unsupported arguments; Kafka MCP uses stdio with no arguments")
	}
	ro := strings.TrimSpace(os.Getenv("KAFKA_MCP_READ_ONLY"))
	if ro != "" && ro != "true" {
		return fmt.Errorf("KAFKA_MCP_READ_ONLY must be true")
	}
	path := strings.TrimSpace(os.Getenv("KAFKA_TARGETS_FILE"))
	if path == "" {
		return fmt.Errorf("set KAFKA_TARGETS_FILE to your private targets JSON file")
	}
	reg := targets.New(path)
	if _, err := reg.List(); err != nil {
		return fmt.Errorf("invalid Kafka targets configuration: %w", err)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "kafka", Version: version}, &mcp.ServerOptions{Instructions: "Read-only Kafka object diagnostics. Resolve local target and allowlists; inspect group, topic, configs and offset boundaries. Message sampling is bounded, no group joins or commits. Report evidence and units before analysis, never infer business code or processing position from committed offsets. No writes or arbitrary shell. Capability support does not establish authorization."})
	tools.Register(srv, tools.Deps{Reg: reg, Factory: func(t targets.Target) (tools.Backend, error) { return broker.New(t) }})
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
