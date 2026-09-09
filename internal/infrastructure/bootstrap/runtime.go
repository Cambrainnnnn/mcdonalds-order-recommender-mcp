package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

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

// Runtime contains the application services shared by all inbound adapters.
// Commands are responsible for closing it when their transport shuts down.
type Runtime struct {
	Recommendations *apprecommendation.Service
	Offers          *appoffers.Service
	Products        *appproducts.Service
	database        *sql.DB
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}

	sessions := memory.NewSessionRepository()
	var offers offerRepository = memory.NewOfferRepository(time.Now, cfg.OfferStaleAfter)
	var products appproducts.Repository = memory.NewProductRepository()
	var idGenerator appproducts.IDGenerator = ids.Default
	var database *sql.DB

	if cfg.DatabaseURL != "" {
		var err error
		database, err = sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		if err := database.PingContext(ctx); err != nil {
			database.Close()
			return nil, fmt.Errorf("connect database: %w", err)
		}
		if err := postgres.Migrate(ctx, database); err != nil {
			database.Close()
			return nil, fmt.Errorf("migrate database: %w", err)
		}
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
	recommendations := apprecommendation.NewService(
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

	return &Runtime{
		Recommendations: recommendations,
		Offers:          offerAdmin,
		Products:        productAdmin,
		database:        database,
	}, nil
}

func (r *Runtime) Close() error {
	if r == nil || r.database == nil {
		return nil
	}
	return r.database.Close()
}
