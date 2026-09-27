package config

import "testing"

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
