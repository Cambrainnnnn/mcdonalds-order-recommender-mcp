package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"meal-recommendation/internal/adapters/inbound/httpapi"
	"meal-recommendation/internal/adapters/outbound/mcdmcp"
	"meal-recommendation/internal/adapters/outbound/memory"
	"meal-recommendation/internal/adapters/outbound/postgres"
	appoffers "meal-recommendation/internal/application/offers"
	appproducts "meal-recommendation/internal/application/products"
	apprecommendation "meal-recommendation/internal/application/recommendation"
	domainrecommendation "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/config"
	"meal-recommendation/internal/infrastructure/ids"
)

type offerRepository interface {
	appoffers.Repository
	apprecommendation.OfferCatalog
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	sessions := memory.NewSessionRepository()
	var offers offerRepository = memory.NewOfferRepository(time.Now, cfg.OfferStaleAfter)
	var products appproducts.Repository = memory.NewProductRepository()
	var idGenerator appproducts.IDGenerator = ids.Default
	var database *sql.DB
	if cfg.DatabaseURL != "" {
		database, err = sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			logger.Error("open database", "error", err)
			os.Exit(1)
		}
		defer database.Close()
		startupContext, cancel := context.WithTimeout(context.Background(), cfg.ExternalTimeout)
		if err := database.PingContext(startupContext); err != nil {
			cancel()
			logger.Error("connect database", "error", err)
			os.Exit(1)
		}
		if err := postgres.Migrate(startupContext, database); err != nil {
			cancel()
			logger.Error("migrate database", "error", err)
			os.Exit(1)
		}
		cancel()
		offers = postgres.NewOfferRepository(database, time.Now, cfg.OfferStaleAfter, cfg.CacheTTL, cfg.CacheStaleTTL)
		products = postgres.NewProductRepository(database)
		idGenerator = postgres.NewIDGenerator(database)
		logger.Info("PostgreSQL offer catalog enabled")
	} else {
		logger.Warn("APP_DATABASE_URL is empty; using in-memory offer catalog")
	}
	mcdHTTPClient := &http.Client{Timeout: cfg.ExternalTimeout}
	mcdClient := mcdmcp.NewClient(cfg.MCDMCPEndpoint, cfg.MCDMCPToken, mcdHTTPClient)
	officialCatalog := mcdmcp.NewCatalog(mcdClient, time.Now, cfg.CacheTTL, cfg.CacheStaleTTL, cfg.MCDMaxDetails)
	officialPricing := mcdmcp.NewPricing(mcdClient, time.Now, min(cfg.CacheTTL, 30*time.Second), cfg.CacheStaleTTL)
	if cfg.MCDMCPToken == "" {
		logger.Warn("APP_MCD_MCP_TOKEN is empty; official channel will degrade to manual offers")
	}
	productAdmin := appproducts.NewService(products, idGenerator, time.Now)
	offerAdmin := appoffers.NewService(offers, idGenerator, time.Now, appoffers.WithProductCatalog(productAdmin))
	service := apprecommendation.NewService(
		sessions,
		idGenerator,
		time.Now,
		cfg.SessionTTL,
		apprecommendation.WithRecommendationDependencies(
			offers,
			officialCatalog,
			officialPricing,
			domainrecommendation.NewEngine(),
			domainrecommendation.ScoringPolicy{Version: "mvp-v1"},
		),
	)

	handler := httpapi.NewHandler(service, logger, httpapi.WithOfferAdmin(offerAdmin), httpapi.WithProductAdmin(productAdmin))
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
