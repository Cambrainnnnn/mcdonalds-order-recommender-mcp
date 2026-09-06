package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	appoffers "meal-recommendation/internal/application/offers"
	domain "meal-recommendation/internal/domain/recommendation"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("APP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("snowflake_test_%d", time.Now().UnixNano())
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		db.Close()
	})
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedLegacy(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{"001_offers.sql", "002_products.sql"} {
		data, _ := migrations.ReadFile("migrations/" + name)
		for _, part := range strings.Split(string(data), "-- migrate:split") {
			if _, err := db.Exec(part); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err := db.Exec(`
 INSERT INTO products VALUES ('manual:mcd:burger','mcd','burger','one','','[]','','active',now(),now());
 INSERT INTO offers VALUES ('old-offer','published',2,'{"id":"old-offer","version":2,"status":"published","title":"Lunch","source":"manual","price":{"amount":1000,"currency":"CNY"},"components":[{"canonicalProductId":"manual:mcd:burger","quantity":1,"choiceGroupId":"main"},{"canonicalProductId":"orphan","quantity":2,"choiceGroupId":"main"}]}',now(),now());
 INSERT INTO offer_versions SELECT 'old-offer-v1',id,1,jsonb_set(draft,'{version}','1'),now(),NULL,NULL,'[]',NULL FROM offers;
 INSERT INTO offer_versions SELECT 'old-offer-v2',id,2,draft,now(),NULL,NULL,'[]',NULL FROM offers;
 `)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationPreservesReferencesAndIsIdempotent(t *testing.T) {
	db := testDatabase(t)
	seedLegacy(t, db)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRow(`SELECT jsonb_agg(row_to_json(o))::text FROM offers o`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT jsonb_agg(row_to_json(o))::text FROM offers o`).Scan(&after); err != nil || before != after {
		t.Fatalf("rerun changed data: %v", err)
	}
	var valid bool
	err := db.QueryRow(`SELECT (SELECT count(*) FROM products)=1 AND (SELECT count(*) FROM offers)=1 AND (SELECT count(*) FROM offer_versions)=2
 AND (SELECT bool_and(snapshot->>'id'=offer_id AND snapshot->>'versionId'=id AND valid_offer_snowflake_ids(snapshot)) FROM offer_versions)
 AND (SELECT bool_and(draft->'components'->0->>'canonicalProductId'=p.id AND draft->'components'->0->>'choiceGroupId'=draft->'components'->1->>'choiceGroupId') FROM offers,products p)
 AND (SELECT count(DISTINCT snapshot->'components'->1->>'canonicalProductId') FROM offer_versions)=1
 AND (SELECT bool_and(snapshot->>'title'='Lunch' AND snapshot->'components'->1->>'quantity'='2') FROM offer_versions)`).Scan(&valid)
	if err != nil || !valid {
		t.Fatalf("migration invariants failed: %v", err)
	}
	for _, query := range []string{`UPDATE offer_versions SET version=99`, `DELETE FROM offer_versions`, `UPDATE products SET id='prod_abcdef'`, `UPDATE offers SET draft=jsonb_set(draft,'{components,0,canonicalProductId}','"manual:mcd:burger"')`} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("accepted forbidden mutation: %s", query)
		}
	}
	repo := NewOfferRepository(db, time.Now, time.Hour, time.Minute, time.Hour)
	eligible, err := repo.ListEligible(ctx, domain.Requirement{})
	if err != nil || len(eligible) != 1 {
		t.Fatalf("eligible=%v err=%v", eligible, err)
	}
	var currentID string
	if err := db.QueryRow(`SELECT id FROM offer_versions WHERE version=2`).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	if eligible[0].ID != currentID {
		t.Fatal("recommendation did not use stored version ID")
	}
	generator := NewIDGenerator(db)
	// Persisted allocator survives replacement of the generator and clock rollback.
	first, err := generator.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE snowflake_state SET last_ms=last_ms+100, sequence=4095`); err != nil {
		t.Fatal(err)
	}
	second, err := NewIDGenerator(db).NewID()
	if err != nil || second <= first {
		t.Fatalf("durable allocation: %s %s %v", first, second, err)
	}
	service := appoffers.NewService(repo, generator, time.Now)
	offer, err := service.Create(ctx, appoffers.CreateCommand{Source: "test", Title: "New", Price: domain.Money{Amount: 1, Currency: "CNY"}, Components: []appoffers.Component{{CanonicalProductID: "123456789012345678", Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(ctx, offer.ID); err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(ctx, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Verify(ctx, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.VersionID == verified.VersionID || published.VersionID == offer.ID {
		t.Fatal("version IDs reused")
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	db := testDatabase(t)
	seedLegacy(t, db)
	// A malformed legacy component makes rewriting fail after protections change.
	if _, err := db.Exec(`UPDATE offers SET draft=jsonb_set(draft,'{components}','[{"quantity":1}]')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err == nil {
		t.Fatal("expected migration failure")
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM products`).Scan(&id); err != nil || id != "manual:mcd:burger" {
		t.Fatalf("partial migration: %s %v", id, err)
	}
	if _, err := db.Exec(`UPDATE offer_versions SET version=99`); err == nil {
		t.Fatal("immutable trigger not restored")
	}
}
