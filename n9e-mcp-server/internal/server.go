package internal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/n9e/n9e-mcp-server/pkg/api"
	"github.com/n9e/n9e-mcp-server/pkg/client"
	"github.com/n9e/n9e-mcp-server/pkg/toolset"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/natefinch/lumberjack.v2"
)

type ServerConfig struct {
	Version         string
	Token           string
	BaseURL         string
	EnabledToolsets []string
	ReadOnly        bool
}

func NewMCPServer(cfg ServerConfig) (*mcp.Server, error) {
	n9eClient, err := client.NewClient(cfg.Token, cfg.BaseURL, fmt.Sprintf("n9e-mcp-server/%s", cfg.Version))
	if err != nil {
		return nil, fmt.Errorf("failed to create n9e client: %w", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "n9e-mcp-server",
		Version: cfg.Version,
	}, &mcp.ServerOptions{
		Instructions: "Nightingale (n9e) read-only MCP. Query active/history alerts, rules, targets, " +
			"business groups, datasources, PromQL metrics, and Loki/ES/OS logs. " +
			"Do not claim to have muted, closed, or modified alerts or rules.",
		Logger: slog.Default(),
	})

	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = client.ContextWithClient(ctx, n9eClient)
			return next(ctx, method, req)
		}
	})

	getClient := func(ctx context.Context) *client.Client {
		return client.ClientFromContext(ctx)
	}
	toolsetGroup := api.DefaultToolsetGroup(getClient, cfg.ReadOnly)

	enabledToolsets := cfg.EnabledToolsets
	if len(enabledToolsets) == 0 {
		enabledToolsets = toolset.DefaultToolsets
	}
	if err := toolsetGroup.EnableToolsets(enabledToolsets); err != nil {
		return nil, fmt.Errorf("failed to enable toolsets: %w", err)
	}
	toolsetGroup.RegisterAll(server)
	return server, nil
}

type StdioServerConfig struct {
	Version         string
	Token           string
	BaseURL         string
	EnabledToolsets []string
	ReadOnly        bool
	LogFilePath     string
}

func RunStdioServer(cfg StdioServerConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var logOutput io.Writer = os.Stderr
	if cfg.LogFilePath != "" {
		logOutput = &lumberjack.Logger{
			Filename:   cfg.LogFilePath,
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     7,
			Compress:   true,
		}
	}

	var logLevel slog.LevelVar
	parseLogLevel := func() slog.Level {
		switch os.Getenv("N9E_MCP_LOG_LEVEL") {
		case "debug", "DEBUG":
			return slog.LevelDebug
		case "warn", "WARN":
			return slog.LevelWarn
		case "error", "ERROR":
			return slog.LevelError
		default:
			return slog.LevelInfo
		}
	}
	logLevel.Set(parseLogLevel())
	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: &logLevel}))
	slog.SetDefault(logger)

	setupSignalReload(func() {
		newLevel := parseLogLevel()
		logLevel.Set(newLevel)
		logger.Info("log level reloaded", "level", newLevel.String())
	})

	logger.Info("starting n9e-mcp-server",
		"version", cfg.Version,
		"base_url", cfg.BaseURL,
		"read_only", cfg.ReadOnly,
		"toolsets", cfg.EnabledToolsets,
	)

	server, err := NewMCPServer(ServerConfig{
		Version:         cfg.Version,
		Token:           cfg.Token,
		BaseURL:         cfg.BaseURL,
		EnabledToolsets: cfg.EnabledToolsets,
		ReadOnly:        cfg.ReadOnly,
	})
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}

	errC := make(chan error, 1)
	go func() {
		errC <- server.Run(ctx, &mcp.StdioTransport{})
	}()

	fmt.Fprintln(os.Stderr, "Nightingale MCP Server running on stdio")

	select {
	case <-ctx.Done():
		logger.Info("shutting down server...")
	case err := <-errC:
		if err != nil {
			logger.Error("server error", "error", err)
			return fmt.Errorf("server error: %w", err)
		}
	}
	return nil
}
