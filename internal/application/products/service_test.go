package products_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"meal-recommendation/internal/adapters/outbound/memory"
	"meal-recommendation/internal/adapters/outbound/postgres"
	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
	"meal-recommendation/internal/infrastructure/ids"
)

func productRepositories(t *testing.T, run func(*testing.T, app.Repository)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, memory.NewProductRepository()) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("APP_TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("APP_TEST_DATABASE_URL is not configured")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		schema := fmt.Sprintf("products_test_%d", time.Now().UnixNano())
		if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
				t.Error(err)
			}
		})
		if _, err := db.Exec(`SET search_path TO ` + schema); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := postgres.Migrate(context.Background(), db); err != nil {
				t.Fatal(err)
			}
		}
		run(t, postgres.NewProductRepository(db))
	})
}

func TestCatalogLifecycleAndUniqueness(t *testing.T) {
	productRepositories(t, func(t *testing.T, repo app.Repository) {
		ctx := context.Background()
		now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
		service := app.NewService(repo, ids.Default, func() time.Time { return now })
		fields := app.Fields{Brand: " MCD ", Name: " 薯条 ", Specification: " 中份 ", Category: "小食", Aliases: []string{" 中薯 ", "中薯", "Medium Fries", "medium fries", ""}}
		command := app.CreateCommand{Fields: fields}
		p, err := service.Create(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if p.Brand != "mcd" || p.Name != "薯条" || p.Specification != "中份" || p.Status != domain.StatusActive || len(p.Aliases) != 2 {
			t.Fatalf("normalization: %+v", p)
		}
		if !p.Supports(domain.SalesScopeStandalone) || !p.Supports(domain.SalesScopeOfferComponent) {
			t.Fatalf("default sales scopes: %+v", p.SalesScopes)
		}
		if fields.Aliases[0] != " 中薯 " {
			t.Fatal("input aliases mutated")
		}
		p.Aliases[0] = "changed"
		stored, err := service.Get(ctx, " "+p.ID+" ")
		if err != nil || stored.Aliases[0] != "中薯" {
			t.Fatalf("stored=%+v err=%v", stored, err)
		}
		// A fresh service reads the repository's durable state, without a service cache.
		reloaded, err := app.NewService(repo, ids.Default, time.Now).Get(ctx, p.ID)
		if err != nil || reloaded.Name != "薯条" {
			t.Fatalf("reload=%+v err=%v", reloaded, err)
		}
		if _, err := service.Create(ctx, command); !errors.Is(err, app.ErrConflict) {
			t.Fatalf("duplicate ID: %v", err)
		}
		command.ID = ""
		if _, err := service.Create(ctx, command); !errors.Is(err, app.ErrConflict) {
			t.Fatalf("duplicate identity: %v", err)
		}
		command.ID = ""
		command.Specification = "大份"
		large, err := service.Create(ctx, command)
		if err != nil || large.ID == p.ID {
			t.Fatalf("different specification: %+v %v", large, err)
		}
		if _, err := service.Update(ctx, large.ID, fields); !errors.Is(err, app.ErrConflict) {
			t.Fatalf("update conflict: %v", err)
		}
		now = now.Add(time.Hour)
		fields.Status = domain.StatusDisabled
		updated, err := service.Update(ctx, p.ID, fields)
		if err != nil || updated.ID != p.ID || !updated.CreatedAt.Equal(p.CreatedAt) || !updated.UpdatedAt.Equal(now) {
			t.Fatalf("update=%+v err=%v", updated, err)
		}
		fields.Status = ""
		updated, err = service.Update(ctx, p.ID, fields)
		if err != nil || updated.Status != domain.StatusDisabled {
			t.Fatalf("omitted status should preserve disabled: %+v %v", updated, err)
		}
		if _, err := service.Get(ctx, "missing"); !errors.Is(err, app.ErrNotFound) {
			t.Fatalf("missing get: %v", err)
		}
		if _, err := service.Update(ctx, "missing", fields); !errors.Is(err, app.ErrNotFound) {
			t.Fatalf("missing update: %v", err)
		}
		if err := repo.Save(ctx, &domain.Product{ID: "missing"}); !errors.Is(err, app.ErrNotFound) {
			t.Fatalf("missing save: %v", err)
		}
	})
}

func TestPriceAndSalesScopes(t *testing.T) {
	productRepositories(t, func(t *testing.T, repo app.Repository) {
		ctx := context.Background()
		service := app.NewService(repo, ids.Default, time.Now)
		price := &domain.Money{Amount: 999900, Currency: " cny "}
		p, err := service.Create(ctx, app.CreateCommand{Fields: app.Fields{
			Brand: "mcd", Name: "活动限定堡", Specification: "1个", Price: price,
			SalesScopes: []domain.SalesScope{domain.SalesScopeOfferComponent, domain.SalesScopeOfferComponent},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Price == nil || p.Price.Amount != 999900 || p.Price.Currency != "CNY" {
			t.Fatalf("price=%+v", p.Price)
		}
		if price.Currency != " cny " {
			t.Fatalf("input price mutated: %+v", price)
		}
		if len(p.SalesScopes) != 1 || !p.Supports(domain.SalesScopeOfferComponent) || p.Supports(domain.SalesScopeStandalone) {
			t.Fatalf("sales scopes=%+v", p.SalesScopes)
		}
		price.Amount = 1
		stored, err := service.Get(ctx, p.ID)
		if err != nil || stored.Price.Amount != 999900 {
			t.Fatalf("stored=%+v err=%v", stored, err)
		}
		for _, fields := range []app.Fields{
			{Brand: "mcd", Name: "无范围", Specification: "1个", SalesScopes: []domain.SalesScope{}},
			{Brand: "mcd", Name: "错误范围", Specification: "1个", SalesScopes: []domain.SalesScope{"delivery"}},
			{Brand: "mcd", Name: "错误价格", Specification: "1个", Price: &domain.Money{Amount: -1, Currency: "CNY"}},
		} {
			if _, err := service.Create(ctx, app.CreateCommand{Fields: fields}); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid fields accepted: %+v err=%v", fields, err)
			}
		}
	})
}

func TestFullCatalogAndFilters(t *testing.T) {
	productRepositories(t, func(t *testing.T, repo app.Repository) {
		ctx := context.Background()
		service := app.NewService(repo, ids.Default, time.Now)
		empty, err := service.List(ctx, app.Filter{})
		if err != nil || empty == nil || len(empty) != 0 {
			t.Fatalf("empty=%v err=%v", empty, err)
		}
		for i := 0; i < 125; i++ {
			status := domain.StatusActive
			if i == 124 {
				status = domain.StatusDisabled
			}
			_, err := service.Create(ctx, app.CreateCommand{Fields: app.Fields{Brand: "mcd", Name: fmt.Sprintf("餐品%03d", i), Specification: "标准份", Category: "小食", Aliases: []string{fmt.Sprintf("alias-%03d", i)}, Status: status}})
			if err != nil {
				t.Fatal(err)
			}
		}
		all, err := service.List(ctx, app.Filter{})
		if err != nil || len(all) != 125 {
			t.Fatalf("full list count=%d err=%v", len(all), err)
		}
		if all[0].Name != "餐品000" || all[124].Name != "餐品124" {
			t.Fatal("list not sorted")
		}
		active, err := service.List(ctx, app.Filter{Brand: "MCD", Category: "小食", Status: domain.StatusActive})
		if err != nil || len(active) != 124 {
			t.Fatalf("active count=%d err=%v", len(active), err)
		}
		found, err := service.List(ctx, app.Filter{Query: "ALIAS-042"})
		if err != nil || len(found) != 1 || found[0].Name != "餐品042" {
			t.Fatalf("alias search: %+v %v", found, err)
		}
		found[0].Aliases[0] = "mutated"
		again, _ := service.List(ctx, app.Filter{Query: "alias-042"})
		if len(again) != 1 {
			t.Fatal("list exposed mutable storage")
		}
		for _, filter := range []app.Filter{{Brand: "other"}, {Category: "饮料"}, {Query: "absent"}} {
			found, err := service.List(ctx, filter)
			if err != nil || len(found) != 0 {
				t.Fatalf("filter=%+v result=%+v err=%v", filter, found, err)
			}
		}
		if _, err := service.List(ctx, app.Filter{Status: "invalid"}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid status: %v", err)
		}
	})
}

func TestRejectInvalidProducts(t *testing.T) {
	service := app.NewService(memory.NewProductRepository(), ids.Default, time.Now)
	for _, command := range []app.CreateCommand{
		{Fields: app.Fields{Name: "薯条", Specification: "中份"}},
		{Fields: app.Fields{Brand: "mcd", Specification: "中份"}},
		{Fields: app.Fields{Brand: "mcd", Name: "薯条"}},
		{Fields: app.Fields{Brand: "mcd", Name: "薯条", Specification: "中份", Status: "draft"}},
		{ID: "bad/id", Fields: app.Fields{Brand: "mcd", Name: "薯条", Specification: "中份"}},
		{Fields: app.Fields{Brand: "mcd", Name: "薯条\x00", Specification: "中份"}},
	} {
		if _, err := service.Create(context.Background(), command); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("command=%+v err=%v", command, err)
		}
	}
}
