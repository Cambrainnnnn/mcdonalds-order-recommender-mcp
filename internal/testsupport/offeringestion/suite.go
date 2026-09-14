// Package offeringestion contains adapter conformance tests for aggregate writes.
package offeringestion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	offers "meal-recommendation/internal/application/offers"
	products "meal-recommendation/internal/application/products"
	rec "meal-recommendation/internal/application/recommendation"
	product "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/ids"
)

func Run(t *testing.T, store offers.IngestionStore, repo offers.Repository, catalog products.Repository, eligible rec.OfferCatalog) {
	t.Helper()
	ctx := context.Background()
	service := offers.NewService(repo, ids.Default, time.Now, offers.WithProductCatalog(catalog), offers.WithIngestionStore(store))
	command := offers.CreateCommand{Source: "meituan", Title: "午餐", Price: domain.Money{Amount: 1000, Currency: "CNY"}, ChoiceGroups: []offers.ChoiceGroup{{ID: "123", MinSelect: 2, MaxSelect: 2, AllowRepeat: true}}, Components: []offers.Component{{Quantity: 2, ChoiceGroupID: "123", ExtraPrice: 100, ExtraPriceMode: "per_unit", MaxRepeat: 2}}}
	bindings := []offers.ProductBinding{{ComponentIndex: 0, Product: offers.ProductInput{Brand: " MCD ", Name: "薯条", Specification: "中份"}}}
	const workers = 8
	results := make(chan *offers.IngestionResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := service.Ingest(ctx, "", command, bindings, "retry")
			if e != nil {
				errs <- e
			} else {
				results <- r
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var first *offers.IngestionResult
	for r := range results {
		if first == nil {
			first = r
		}
		if r.Offer.ID != first.Offer.ID || len(r.Products) != 1 || !r.Products[0].Created {
			t.Fatalf("inconsistent replay: %+v", r)
		}
	}
	all, _ := repo.List(ctx)
	ps, _ := catalog.List(ctx)
	if len(all) != 1 || len(ps) != 1 {
		t.Fatalf("retry leaked rows: offers=%d products=%d", len(all), len(ps))
	}
	if first.Offer.Status != offers.StatusDraft || ps[0].Supports(product.SalesScopeStandalone) {
		t.Fatal("unexpected publication or standalone sale")
	}
	changed := command
	changed.Title = "changed"
	if _, e := service.Ingest(ctx, "", changed, bindings, "retry"); !errors.Is(e, offers.ErrIdempotencyConflict) {
		t.Fatalf("mismatch: %v", e)
	}
	if _, e := service.Ingest(ctx, "", command, bindings, "duplicate"); !errors.Is(e, offers.ErrOfferConflict) {
		t.Fatalf("business duplicate: %v", e)
	}
	// Different request keys must still be protected by business identity.
	concurrent := command
	concurrent.Title = "concurrent identity"
	conflictErrors := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := service.Ingest(ctx, "", concurrent, bindings, "")
			conflictErrors <- e
		}()
	}
	wg.Wait()
	close(conflictErrors)
	successes, conflicts := 0, 0
	for e := range conflictErrors {
		if e == nil {
			successes++
		} else if errors.Is(e, offers.ErrOfferConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent dedup successes=%d conflicts=%d", successes, conflicts)
	}
	// A failed duplicate must not reserve the idempotency key.
	reused, e := service.Ingest(ctx, "", changed, bindings, "duplicate")
	if e != nil || reused.Products[0].Created || reused.Products[0].ProductID != ps[0].ID {
		t.Fatalf("reuse: %+v %v", reused, e)
	}
	// Invalid selections are detected after product creation, then rolled back.
	bad := command
	bad.Title = "bad"
	bad.ChoiceGroups = []offers.ChoiceGroup{{ID: "123", MinSelect: 3, MaxSelect: 3}}
	missing := []offers.ProductBinding{{ComponentIndex: 0, Product: offers.ProductInput{Brand: "mcd", Name: "new", Specification: "one"}}}
	if _, e := service.Ingest(ctx, "", bad, missing, "failed"); !errors.Is(e, offers.ErrInvalidOffer) {
		t.Fatalf("invalid accepted: %v", e)
	}
	ps, _ = catalog.List(ctx)
	if len(ps) != 1 {
		t.Fatal("failed write left a product")
	}
	// External channel identity wins even when content differs; new products roll back.
	ext := command
	ext.ExternalID = "sku"
	ext.Title = "external"
	external, e := service.Ingest(ctx, "", ext, bindings, "")
	if e != nil {
		t.Fatal(e)
	}
	ext.Title = "external changed"
	if _, e := service.Ingest(ctx, "", ext, missing, ""); !errors.Is(e, offers.ErrOfferConflict) {
		t.Fatalf("external duplicate: %v", e)
	}
	ps, _ = catalog.List(ctx)
	if len(ps) != 1 {
		t.Fatal("duplicate left a product")
	}
	// Draft updates preserve ID/version, and update retries remain immutable.
	update := command
	update.Title = "edited"
	saved, e := service.Ingest(ctx, first.Offer.ID, update, bindings, "edit")
	if e != nil || saved.Offer.ID != first.Offer.ID {
		t.Fatalf("update: %v", e)
	}
	if _, e = service.Review(ctx, first.Offer.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Publish(ctx, first.Offer.ID); e != nil {
		t.Fatal(e)
	}
	replay, e := service.Ingest(ctx, first.Offer.ID, update, bindings, "edit")
	if e != nil || replay.Offer.Status != offers.StatusDraft {
		t.Fatalf("replay after publish: %v", e)
	}
	if _, e := service.Ingest(ctx, first.Offer.ID, update, bindings, "new-edit"); !errors.Is(e, offers.ErrInvalidTransition) {
		t.Fatalf("published edit: %v", e)
	}
	versions, e := eligible.ListEligible(ctx, domain.Requirement{})
	if e != nil || len(versions) != 1 {
		t.Fatalf("eligible: %v %v", versions, e)
	}
	if !versions[0].ChoiceGroups[0].AllowRepeat || versions[0].Components[0].ExtraPriceMode != "per_unit" || versions[0].Components[0].MaxRepeat != 2 {
		t.Fatal("mapping lost selection rules")
	}
	// Full recommendation service must charge 2 selections * 2 units * 100.
	recService := rec.NewService(memory.NewSessionRepository(), ids.Default, time.Now, time.Hour, rec.WithLocalCatalogDependencies(eligible, catalog, domain.NewEngine(), domain.ScoringPolicy{Version: "test"}))
	session, e := recService.StartSession(ctx, rec.StartSessionCommand{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = recService.UpdateRequirement(ctx, session.ID, domain.Requirement{MustHave: []domain.ProductRequest{{ResolvedProductID: ps[0].ID, Quantity: 1}}}); e != nil {
		t.Fatal(e)
	}
	recommendations, e := recService.GenerateRecommendations(ctx, session.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(recommendations.Recommendations) == 0 || recommendations.Recommendations[0].Candidate.TotalPrice.Amount != 1400 {
		t.Fatalf("published price: %+v", recommendations)
	}
	// Verify reference-only ingestion does not guess missing identities.
	referenced := command
	referenced.Title = "missing reference"
	referenced.Components = []offers.Component{{CanonicalProductID: "999", Quantity: 1}}
	referenced.ChoiceGroups = nil
	if _, e := service.Ingest(ctx, "", referenced, nil, ""); !errors.Is(e, offers.ErrInvalidOffer) {
		t.Fatalf("missing reference: %v", e)
	}
	// Disabled catalog entries remain reusable without changing state.
	ps[0].Status = product.StatusDisabled
	if e := catalog.Save(ctx, ps[0]); e != nil {
		t.Fatal(e)
	}
	disabled := command
	disabled.Title = "disabled reuse"
	r, e := service.Ingest(ctx, "", disabled, bindings, "")
	if e != nil || r.Products[0].Created {
		t.Fatalf("disabled reuse: %v", e)
	}
	p, _ := catalog.Get(ctx, ps[0].ID)
	if p.Status != product.StatusDisabled {
		t.Fatal("reuse modified product")
	}
	_ = external
}
