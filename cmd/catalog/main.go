// Command catalog exports a consistent business catalog without modifying the database.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"meal-recommendation/internal/adapters/outbound/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "catalog:", err)
		os.Exit(1)
	}
}

func run() error {
	output := flag.String("output", "data/catalog.json", "output JSON file, or - for stdout")
	timeout := flag.Duration("timeout", time.Minute, "database export timeout")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		return fmt.Errorf("usage: catalog [-output data/catalog.json] [-timeout 1m]")
	}
	dsn := strings.TrimSpace(os.Getenv("APP_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		return fmt.Errorf("APP_DATABASE_URL or DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	c, err := postgres.ExportCatalog(ctx, db)
	if err != nil {
		return err
	}
	if *output == "-" {
		return c.Write(os.Stdout)
	}
	// Replace only after a complete write; failed exports keep the previous snapshot.
	if err = os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(*output), ".catalog-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = c.Write(f); err != nil {
		return err
	}
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), *output); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "exported %d products, %d offers, %d versions to %s\n", len(c.Products), len(c.Offers), len(c.Versions), *output)
	return nil
}
