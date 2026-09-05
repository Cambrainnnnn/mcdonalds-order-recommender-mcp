package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	HTTPAddress     string
	SessionTTL      time.Duration
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	sessionTTL, err := durationFromEnv("APP_SESSION_TTL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationFromEnv("APP_SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	address := os.Getenv("APP_HTTP_ADDRESS")
	if address == "" {
		address = ":8080"
	}

	return Config{
		HTTPAddress:     address,
		SessionTTL:      sessionTTL,
		ShutdownTimeout: shutdownTimeout,
	}, nil
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
