package config

import "testing"

func TestCatalogSeedConfiguration(t *testing.T) {
	t.Setenv("APP_CATALOG_SEED_MODE", "")
	t.Setenv("APP_CATALOG_SEED_PATH", "")
	t.Setenv("APP_STARTUP_TIMEOUT", "")
	cfg, err := Load()
	if err != nil || cfg.CatalogSeedMode != "off" || cfg.StartupTimeout.Seconds() != 60 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("APP_CATALOG_SEED_MODE", "merge")
	if _, err := Load(); err == nil {
		t.Fatal("accepted missing path")
	}
	t.Setenv("APP_CATALOG_SEED_PATH", "data/catalog.json")
	for _, mode := range []string{"merge", "if-empty", "off"} {
		t.Setenv("APP_CATALOG_SEED_MODE", mode)
		if _, err := Load(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("APP_CATALOG_SEED_MODE", "replace")
	if _, err := Load(); err == nil {
		t.Fatal("accepted invalid mode")
	}
	t.Setenv("APP_CATALOG_SEED_MODE", "off")
	t.Setenv("APP_STARTUP_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("accepted zero startup timeout")
	}
}

func TestRailwayEnvironment(t *testing.T) {
	t.Setenv("APP_HTTP_ADDRESS", "")
	t.Setenv("APP_DATABASE_URL", "")
	t.Setenv("PORT", "9000")
	t.Setenv("DATABASE_URL", " postgres://railway/db ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != ":9000" || cfg.DatabaseURL != "postgres://railway/db" {
		t.Fatalf("unexpected Railway config: %+v", cfg)
	}
	t.Setenv("APP_HTTP_ADDRESS", "127.0.0.1:8081")
	t.Setenv("APP_DATABASE_URL", "postgres://explicit/db")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != "127.0.0.1:8081" || cfg.DatabaseURL != "postgres://explicit/db" {
		t.Fatal("explicit APP configuration should win")
	}
	t.Setenv("APP_HTTP_ADDRESS", "")
	t.Setenv("APP_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PORT", "")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddress != ":8080" || cfg.DatabaseURL != "" {
		t.Fatal("local defaults changed")
	}
	for _, port := range []string{"0", "65536", "-1", "abc"} {
		t.Setenv("PORT", port)
		if _, err := Load(); err == nil {
			t.Errorf("accepted invalid port %q", port)
		}
	}
}
