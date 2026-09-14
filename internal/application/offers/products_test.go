package offers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	offers "meal-recommendation/internal/application/offers"
	products "meal-recommendation/internal/application/products"
	product "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/ids"
)

func TestOfferProductReferences(t *testing.T) {
	ctx := context.Background()
	catalog := products.NewService(memory.NewProductRepository(), ids.Default, time.Now)
	repo := memory.NewOfferRepository(time.Now, time.Hour)
	service := offers.NewService(repo, ids.Default, time.Now, offers.WithProductCatalog(catalog))
	command := offers.CreateCommand{Source: "douyin", Title: "中薯套餐", Price: domain.Money{Amount: 1000, Currency: "CNY"}, Components: []offers.Component{{CanonicalProductID: "MANUAL:MCD:FRIES_MEDIUM", Quantity: 1}}}
	if _, err := service.Create(ctx, command); !errors.Is(err, offers.ErrInvalidOffer) {
		t.Fatalf("unknown product accepted: %v", err)
	}
	fields := products.Fields{Brand: "mcd", Name: "薯条", Specification: "中份"}
	p, err := catalog.Create(ctx, products.CreateCommand{Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	command.Components[0].CanonicalProductID = " " + p.ID + " "
	offer, err := service.Create(ctx, command)
	if err != nil || offer.Components[0].CanonicalProductID != p.ID {
		t.Fatalf("reference=%+v err=%v", offer, err)
	}
	if command.Components[0].CanonicalProductID != " "+p.ID+" " {
		t.Fatal("command mutated")
	}
	fields.SalesScopes = []product.SalesScope{product.SalesScopeStandalone}
	if _, err := catalog.Update(ctx, p.ID, fields); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, command); !errors.Is(err, offers.ErrInvalidOffer) {
		t.Fatalf("standalone-only product accepted as offer component: %v", err)
	}
	fields.SalesScopes = []product.SalesScope{product.SalesScopeOfferComponent}
	if _, err := catalog.Update(ctx, p.ID, fields); err != nil {
		t.Fatal(err)
	}
	command.Title += "新套餐"
	if _, err := service.Create(ctx, command); err != nil {
		t.Fatalf("offer-only product rejected: %v", err)
	}
	invalid := command
	invalid.Components = []offers.Component{{CanonicalProductID: "unknown", Quantity: 1}}
	if _, err := service.UpdateDraft(ctx, offer.ID, invalid); !errors.Is(err, offers.ErrInvalidOffer) {
		t.Fatalf("invalid update accepted: %v", err)
	}
	command.Title += "导入"
	imported := service.Import(ctx, []offers.CreateCommand{command, invalid})
	if len(imported.Created) != 1 || len(imported.Errors) != 1 || imported.Errors[0].Row != 3 {
		t.Fatalf("import=%+v", imported)
	}
	fields.Status = product.StatusDisabled
	if _, err := catalog.Update(ctx, p.ID, fields); err != nil {
		t.Fatal(err)
	}
	command.Title += "新套餐"
	if _, err := service.Create(ctx, command); err != nil {
		t.Fatalf("disabled product rejected during create: %v", err)
	}
	command.Title += "修改"
	if _, err := service.UpdateDraft(ctx, offer.ID, command); err != nil {
		t.Fatalf("disabled product rejected during update: %v", err)
	}
	if _, err := service.Review(ctx, offer.ID); err != nil {
		t.Fatalf("disabled product rejected during review: %v", err)
	}
	if _, err := service.Publish(ctx, offer.ID); err != nil {
		t.Fatalf("disabled product rejected during publish: %v", err)
	}
	if _, err := service.Verify(ctx, offer.ID); err != nil {
		t.Fatalf("disabled product rejected during verify: %v", err)
	}
	// Product lifecycle state is independent from offer lifecycle state.
	if _, err := service.Disable(ctx, offer.ID); err != nil {
		t.Fatal(err)
	}
}
