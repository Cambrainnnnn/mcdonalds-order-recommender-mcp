package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"meal-recommendation/internal/adapters/inbound/mcpserver"
	"meal-recommendation/internal/infrastructure/bootstrap"
	"meal-recommendation/internal/infrastructure/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("MCP HTTP server stopped", "error", err)
		os.Exit(1)
	}
}

func validateConfig(cfg config.Config, token string) error {
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("APP_MCP_TOKEN is required and must not contain whitespace")
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("APP_DATABASE_URL or DATABASE_URL is required for MCP HTTP")
	}
	return nil
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token := os.Getenv("APP_MCP_TOKEN")
	if err := validateConfig(cfg, token); err != nil {
		return err
	}
	startupContext, startupCancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	runtime, err := bootstrap.New(startupContext, cfg, logger)
	startupCancel()
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	defer runtime.Close()

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           newHandler(mcpserver.New(runtime.Recommendations, runtime.Products), token),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("MCP HTTP server started", "address", cfg.HTTPAddress, "path", "/mcp")
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func newHandler(server *mcp.Server, token string) http.Handler {
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	want := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	// Railway probes this only after database initialization and migrations succeed.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Values("Authorization")
		var scheme, credential string
		if len(authorization) == 1 {
			scheme, credential, _ = strings.Cut(authorization[0], " ")
		}
		got := sha256.Sum256([]byte(credential))
		if token == "" || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		// Browser cross-origin clients require an explicit future allowlist.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		stream.ServeHTTP(w, r)
	}))
	return mux
}
