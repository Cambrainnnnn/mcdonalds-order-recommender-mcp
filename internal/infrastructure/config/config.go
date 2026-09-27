package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	CatalogSeedPath string
	CatalogSeedMode string
	StartupTimeout  time.Duration
	HTTPAddress     string
	SessionTTL      time.Duration
	ShutdownTimeout time.Duration
	DatabaseURL     string
	MCDMCPEndpoint  string
	MCDMCPToken     string
	ExternalTimeout time.Duration
	CacheTTL        time.Duration
	CacheStaleTTL   time.Duration
	OfferStaleAfter time.Duration
	MCDMaxDetails   int
}

func Load() (Config, error) {
	seedMode := strings.TrimSpace(os.Getenv("APP_CATALOG_SEED_MODE"))
	if seedMode == "" {
		seedMode = "off"
	}
	if seedMode != "off" && seedMode != "if-empty" && seedMode != "merge" {
		return Config{}, fmt.Errorf("APP_CATALOG_SEED_MODE must be off, if-empty or merge")
	}
	seedPath := strings.TrimSpace(os.Getenv("APP_CATALOG_SEED_PATH"))
	if seedMode != "off" && seedPath == "" {
		return Config{}, fmt.Errorf("APP_CATALOG_SEED_PATH is required when catalog seeding is enabled")
	}
	startupTimeout, err := durationFromEnv("APP_STARTUP_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	sessionTTL, err := durationFromEnv("APP_SESSION_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationFromEnv("APP_SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	externalTimeout, err := durationFromEnv("APP_EXTERNAL_TIMEOUT", 12*time.Second)
	if err != nil {
		return Config{}, err
	}
	cacheTTL, err := durationFromEnv("APP_CACHE_TTL", 2*time.Minute)
	if err != nil {
		return Config{}, err
	}
	cacheStaleTTL, err := durationFromEnv("APP_CACHE_STALE_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	if cacheStaleTTL < cacheTTL {
		return Config{}, fmt.Errorf("APP_CACHE_STALE_TTL must be greater than or equal to APP_CACHE_TTL")
	}
	offerStaleAfter, err := durationFromEnv("APP_OFFER_STALE_AFTER", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	maxDetails, err := positiveIntFromEnv("APP_MCD_MAX_DETAILS", 20)
	if err != nil {
		return Config{}, err
	}

	address := os.Getenv("APP_HTTP_ADDRESS")
	if address == "" {
		address = ":8080"
		if port := os.Getenv("PORT"); port != "" {
			parsed, err := strconv.Atoi(port)
			if err != nil || parsed < 1 || parsed > 65535 {
				return Config{}, fmt.Errorf("PORT must be an integer between 1 and 65535")
			}
			address = ":" + port
		}
	}
	databaseURL := strings.TrimSpace(os.Getenv("APP_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	mcdEndpoint := os.Getenv("APP_MCD_MCP_ENDPOINT")
	if mcdEndpoint == "" {
		mcdEndpoint = "https://mcp.mcd.cn"
	}

	return Config{
		CatalogSeedPath: seedPath,
		CatalogSeedMode: seedMode,
		StartupTimeout:  startupTimeout,
		HTTPAddress:     address,
		SessionTTL:      sessionTTL,
		ShutdownTimeout: shutdownTimeout,
		DatabaseURL:     databaseURL,
		MCDMCPEndpoint:  mcdEndpoint,
		MCDMCPToken:     strings.TrimSpace(os.Getenv("APP_MCD_MCP_TOKEN")),
		ExternalTimeout: externalTimeout,
		CacheTTL:        cacheTTL,
		CacheStaleTTL:   cacheStaleTTL,
		OfferStaleAfter: offerStaleAfter,
		MCDMaxDetails:   maxDetails,
	}, nil
}

func positiveIntFromEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return duration, nil
}
