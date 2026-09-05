package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meal-recommendation/internal/adapters/inbound/httpapi"
	"meal-recommendation/internal/adapters/outbound/memory"
	"meal-recommendation/internal/adapters/outbound/placeholder"
	apprecommendation "meal-recommendation/internal/application/recommendation"
	domainrecommendation "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/config"
	"meal-recommendation/internal/infrastructure/ids"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	sessions := memory.NewSessionRepository()
	service := apprecommendation.NewService(
		sessions,
		ids.NewRandomGenerator(),
		time.Now,
		cfg.SessionTTL,
		apprecommendation.WithRecommendationDependencies(
			placeholder.OfferCatalog{},
			placeholder.OfficialCatalog{},
			placeholder.OfficialPricing{},
			domainrecommendation.NewEngine(),
			domainrecommendation.ScoringPolicy{Version: "mvp-v1"},
		),
	)

	handler := httpapi.NewHandler(service, logger)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server started", "address", cfg.HTTPAddress)
		serverErrors <- server.ListenAndServe()
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case <-shutdownSignal.Done():
		logger.Info("shutdown requested")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
		return
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("http server stopped")
}
