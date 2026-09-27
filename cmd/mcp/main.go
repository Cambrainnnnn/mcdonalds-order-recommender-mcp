package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"meal-recommendation/internal/adapters/inbound/mcpserver"
	"meal-recommendation/internal/infrastructure/bootstrap"
	"meal-recommendation/internal/infrastructure/config"
)

func main() {
	// A stdio MCP server must reserve stdout for JSON-RPC frames.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupContext, startupCancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	runtime, err := bootstrap.New(startupContext, cfg, logger)
	startupCancel()
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}
	defer runtime.Close()

	server := mcpserver.New(runtime.Recommendations, runtime.Products)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		logger.Error("MCP server stopped", "error", err)
		os.Exit(1)
	}
}
