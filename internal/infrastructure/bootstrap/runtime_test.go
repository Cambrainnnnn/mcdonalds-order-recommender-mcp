package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"meal-recommendation/internal/adapters/outbound/postgres"
	"meal-recommendation/internal/infrastructure/config"
)

func TestSeedRequiresDatabase(t *testing.T) {
	_, err := New(context.Background(), config.Config{CatalogSeedMode: "merge", CatalogSeedPath: "missing.json"}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("unexpected error: %v", err)
	}
	r, err := New(context.Background(), config.Config{SessionTTL: time.Minute, CatalogSeedMode: "off", CatalogSeedPath: "missing.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestStartupCatalog(t *testing.T) {
	dsn := os.Getenv("APP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("catalog_startup_%d", time.Now().UnixNano())
	if _, err = db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	conn, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn.RuntimeParams["search_path"] = schema
	registered := stdlib.RegisterConnConfig(conn)
	defer stdlib.UnregisterConnConfig(registered)
	cfg := config.Config{SessionTTL: time.Minute, DatabaseURL: registered, CatalogSeedMode: "if-empty", CatalogSeedPath: filepath.Join("..", "..", "..", "data", "catalog.json")}
	fixture, err := postgres.ReadCatalog(cfg.CatalogSeedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"if-empty", "merge", "if-empty"} {
		cfg.CatalogSeedMode = mode
		r, err := New(ctx, cfg, nil)
		if err != nil {
			t.Fatalf("startup %s: %v", mode, err)
		}
		after, err := postgres.ExportCatalog(ctx, r.database)
		if err != nil {
			r.Close()
			t.Fatal(err)
		}
		if len(after.Products) != len(fixture.Products) || len(after.Offers) != len(fixture.Offers) || len(after.Versions) != len(fixture.Versions) {
			r.Close()
			t.Fatal("startup lost catalog records")
		}
		var a, b strings.Builder
		if err := fixture.Write(&a); err != nil {
			t.Fatal(err)
		}
		if err := after.Write(&b); err != nil {
			t.Fatal(err)
		}
		if a.String() != b.String() {
			r.Close()
			t.Fatal("bundled catalog round trip changed data")
		}
		r.Close()
	}
	cfg.CatalogSeedPath = filepath.Join(t.TempDir(), "missing.json")
	if _, err := New(ctx, cfg, nil); err == nil {
		t.Fatal("enabled startup accepted missing seed")
	}
	cfg.CatalogSeedMode = "off"
	r, err := New(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}
