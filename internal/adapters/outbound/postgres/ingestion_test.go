package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"meal-recommendation/internal/testsupport/offeringestion"
)

func TestIngestion(t *testing.T) {
	db := testDatabase(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("APP_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*cfg)
	pool.SetMaxOpenConns(10)
	t.Cleanup(func() { pool.Close() })
	o := NewOfferRepository(pool, time.Now, time.Hour, 0, 0)
	p := NewProductRepository(pool)
	offeringestion.Run(t, o, o, p, o)
	// A fresh repository instance must replay the persisted request record.
	tx, err := pool.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scoped := ingestionTx{OfferRepository: &OfferRepository{db: pool, tx: tx}}
	record, err := scoped.Request(context.Background(), "retry")
	if err != nil || record == nil || len(record.Response) == 0 {
		t.Fatalf("durable replay record: %v", err)
	}
}
