package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	offers "meal-recommendation/internal/application/offers"
	"meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

func catalogFixture() *Catalog {
	t := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	p := &product.Product{ID: "8000000000000000000", Brand: "mcd", Name: "Burger", Specification: "one", Aliases: []string{}, SalesScopes: []product.SalesScope{product.SalesScopeOfferComponent}, Status: product.StatusActive, CreatedAt: t, UpdatedAt: t}
	o := &offers.Offer{ID: "123", VersionID: "124", Version: 1, Status: offers.StatusPublished, Source: "manual", Title: "Lunch", Price: domain.Money{Amount: 1990, Currency: "CNY"}, Components: []offers.Component{{CanonicalProductID: p.ID, Quantity: 1}}, CreatedAt: t, UpdatedAt: t, VerifiedAt: &t}
	return &Catalog{FormatVersion: 1, Products: []*product.Product{p}, Offers: []*offers.Offer{o}, Versions: []CatalogVersion{{ID: o.VersionID, OfferID: o.ID, Version: 1, Snapshot: *o, PublishedAt: t, VerifiedAt: &t}}}
}

func catalogBytes(t *testing.T, c *Catalog) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := c.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCatalogValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Catalog){
		"format":                  func(c *Catalog) { c.FormatVersion = 2 },
		"missing array":           func(c *Catalog) { c.Products = nil },
		"duplicate product":       func(c *Catalog) { c.Products = append(c.Products, c.Products[0]) },
		"missing product":         func(c *Catalog) { c.Products = []*product.Product{} },
		"missing history":         func(c *Catalog) { c.Versions = []CatalogVersion{} },
		"duplicate version":       func(c *Catalog) { c.Versions = append(c.Versions, c.Versions[0]) },
		"wrong snapshot identity": func(c *Catalog) { c.Versions[0].Snapshot.VersionID = "999" },
		"invalid price":           func(c *Catalog) { c.Offers[0].Price.Amount = -1 },
		"invalid quantity":        func(c *Catalog) { c.Offers[0].Components[0].Quantity = 0 },
		"invalid timestamp":       func(c *Catalog) { c.Products[0].CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			c := catalogFixture()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
	data := catalogBytes(t, catalogFixture())
	for _, input := range []string{string(data) + "{}", strings.Replace(string(data), `"formatVersion": 1`, `"formatVersion": 1, "unexpected": true`, 1), `null`} {
		if _, err := DecodeCatalog(strings.NewReader(input)); err == nil {
			t.Fatal("accepted malformed document")
		}
	}
	c, err := DecodeCatalog(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, catalogBytes(t, c)) {
		t.Fatal("round trip changed snapshot")
	}
}

func TestCatalogImportExportAndRecommendation(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	c := catalogFixture()
	if loaded, err := ImportCatalog(ctx, db, c, "if-empty"); err != nil || !loaded {
		t.Fatalf("initial import: %v %v", loaded, err)
	}
	exported, err := ExportCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want := catalogBytes(t, c)
	if !bytes.Equal(want, catalogBytes(t, exported)) {
		t.Fatal("restore did not preserve full snapshot")
	}
	for _, mode := range []string{"merge", "merge", "if-empty"} {
		loaded, err := ImportCatalog(ctx, db, c, mode)
		if err != nil || loaded != (mode == "merge") {
			t.Fatalf("repeat %s: %v %v", mode, loaded, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM offer_versions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate history: %d %v", count, err)
	}
	id, err := NewIDGenerator(db).NewID()
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	if n <= 8000000000000000000 {
		t.Fatal("allocator did not advance beyond imported IDs")
	}
	eligible, err := NewOfferRepository(db, time.Now, 24*time.Hour, time.Minute, time.Minute).ListEligible(ctx, domain.Requirement{})
	if err != nil || len(eligible) != 1 || eligible[0].Price.Amount.Amount != 1990 {
		t.Fatalf("restored recommendation catalog: %+v %v", eligible, err)
	}
	if !eligible[0].Price.QuotedAt.Equal(*c.Offers[0].VerifiedAt) {
		t.Fatal("import refreshed verification time")
	}
	// A later catalog adds a published version and changes mutable product metadata.
	c.Products[0].Description = "new description"
	c.Products[0].UpdatedAt = c.Products[0].UpdatedAt.Add(time.Hour)
	o := c.Offers[0]
	o.Version = 2
	o.VersionID = "125"
	o.Price.Amount = 2090
	o.UpdatedAt = o.UpdatedAt.Add(time.Hour)
	c.Versions = append(c.Versions, CatalogVersion{ID: o.VersionID, OfferID: o.ID, Version: 2, Snapshot: *o, PublishedAt: o.UpdatedAt, VerifiedAt: o.VerifiedAt})
	if _, err := ImportCatalog(ctx, db, c, "merge"); err != nil {
		t.Fatal(err)
	}
	after, err := ExportCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(catalogBytes(t, c), catalogBytes(t, after)) {
		t.Fatal("incremental update lost data")
	}
	if _, err := ImportCatalog(ctx, db, exported, "merge"); err == nil {
		t.Fatal("stale snapshot overwrote newer data")
	}
	// Omitting records does not delete or disable them.
	empty := &Catalog{FormatVersion: 1, Products: []*product.Product{}, Offers: []*offers.Offer{}, Versions: []CatalogVersion{}}
	if _, err := ImportCatalog(ctx, db, empty, "merge"); err != nil {
		t.Fatal(err)
	}
	still, err := ExportCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(catalogBytes(t, after), catalogBytes(t, still)) {
		t.Fatal("absent records were changed")
	}
}

func TestCatalogConflictRollsBack(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	original := catalogFixture()
	if _, err := ImportCatalog(ctx, db, original, "merge"); err != nil {
		t.Fatal(err)
	}
	before, err := ExportCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	conflict := catalogFixture()
	conflict.Products[0].Description = "must roll back"
	conflict.Products[0].UpdatedAt = conflict.Products[0].UpdatedAt.Add(time.Hour)
	conflict.Versions[0].Snapshot.Title = "rewritten history"
	if _, err := ImportCatalog(ctx, db, conflict, "merge"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected immutable conflict, got %v", err)
	}
	after, err := ExportCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(catalogBytes(t, before), catalogBytes(t, after)) {
		t.Fatal("failed import partially committed")
	}
	conflict = catalogFixture()
	conflict.Products[0].Description = "same timestamp, different content"
	if _, err := ImportCatalog(ctx, db, conflict, "merge"); err == nil {
		t.Fatal("equal-time conflict accepted")
	}
	conflict = catalogFixture()
	conflict.Offers[0].ID, conflict.Offers[0].VersionID = "456", "457"
	conflict.Versions[0].ID, conflict.Versions[0].OfferID = "457", "456"
	conflict.Versions[0].Snapshot = *conflict.Offers[0]
	if _, err := ImportCatalog(ctx, db, conflict, "merge"); err == nil || !strings.Contains(err.Error(), "business identity") {
		t.Fatalf("accepted duplicate business identity: %v", err)
	}
	var keys int
	if err := db.QueryRow(`SELECT count(*) FROM offer_business_keys`).Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("business key reconstruction: %d %v", keys, err)
	}
	// Database protection remains active, even after imports.
	if _, err := db.Exec(`UPDATE offer_versions SET snapshot=$1 WHERE id='124'`, json.RawMessage(`{}`)); err == nil {
		t.Fatal("immutable trigger was bypassed")
	}
}
