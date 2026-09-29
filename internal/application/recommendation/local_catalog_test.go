package recommendation_test

import (
	"context"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	appoffers "meal-recommendation/internal/application/offers"
	app "meal-recommendation/internal/application/recommendation"
	product "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

type localProducts []*product.Product

func (p localProducts) List(context.Context) ([]*product.Product, error) { return p, nil }

type localOffers struct{ t *testing.T }

func (o localOffers) ListEligible(_ context.Context, r domain.Requirement) ([]app.OfferVersion, error) {
	if r.Location != (domain.Location{}) {
		o.t.Fatal("local recommendation forwarded store information")
	}
	return []app.OfferVersion{{ID: "combo", Source: "maintained", Price: domain.PriceQuote{Amount: domain.Money{Amount: 990, Currency: "CNY"}, Confidence: domain.PriceVerifiedPast}, Components: []app.OfferComponent{{CanonicalProductID: "burger", Quantity: 1}}}}, nil
}

func TestLocalRecommendationsWithoutStoreOrOrderMode(t *testing.T) {
	products := localProducts{
		{ID: "burger", Status: product.StatusDisabled, SalesScopes: []product.SalesScope{product.SalesScopeStandalone, product.SalesScopeOfferComponent}, Price: &product.Money{Amount: 1, Currency: "CNY"}},
		{ID: "soy", Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeStandalone}, Price: &product.Money{Amount: 500, Currency: "CNY"}},
		{ID: "unpriced", Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeStandalone}},
		{ID: "component-only", Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeOfferComponent}, Price: &product.Money{Amount: 1, Currency: "CNY"}},
	}
	for _, tc := range []struct {
		name  string
		wants []domain.ProductRequest
		price int64
		fail  bool
	}{
		{"disabled burger through combo plus standalone soy", []domain.ProductRequest{{ResolvedProductID: "burger", Quantity: 1}, {ResolvedProductID: "soy", Quantity: 1}}, 1490, false},
		{"standalone only", []domain.ProductRequest{{ResolvedProductID: "soy", Quantity: 2}}, 1000, false},
		{"no invented price", []domain.ProductRequest{{ResolvedProductID: "unpriced", Quantity: 1}}, 0, true},
		{"component cannot sell alone", []domain.ProductRequest{{ResolvedProductID: "component-only", Quantity: 1}}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := app.NewService(memory.NewSessionRepository(), fixedID("local-session"), time.Now, time.Hour, app.WithLocalCatalogDependencies(localOffers{t}, products, domain.NewEngine(), domain.ScoringPolicy{}))
			ctx := context.Background()
			session, err := service.StartSession(ctx, app.StartSessionCommand{})
			if err != nil {
				t.Fatal(err)
			}
			ready, err := service.UpdateRequirement(ctx, session.ID, domain.Requirement{MustHave: tc.wants})
			if err != nil || ready.State != domain.SessionReady {
				t.Fatalf("ready=%+v err=%v", ready, err)
			}
			result, err := service.GenerateRecommendations(ctx, session.ID)
			if tc.fail {
				if err != app.ErrNoCandidates {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Recommendations) == 0 || result.Recommendations[0].Candidate.TotalPrice.Amount != tc.price {
				t.Fatalf("result=%+v", result)
			}
			for _, r := range result.Recommendations {
				for _, leg := range r.Candidate.PurchaseLegs {
					if leg.Channel == domain.OfficialChannel {
						t.Fatal("local price labeled official")
					}
				}
			}
		})
	}
}

func TestLocalRecommendationAtBeijingLunchWithoutStandalonePrices(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC) // noon in Beijing
	clock := func() time.Time { return now }
	offers := memory.NewOfferRepository(clock, 24*time.Hour)
	from, until := now.Add(-time.Hour), now.Add(time.Hour)
	offer := &appoffers.Offer{
		ID: "lunch", VersionID: "lunch-v1", Version: 1, Status: appoffers.StatusPublished,
		Source: "maintained", Title: "汉堡鸡翅午餐", Price: domain.Money{Amount: 2000, Currency: "CNY"},
		ValidFrom: &from, ValidUntil: &until,
		ValidTimes: []appoffers.ValidTimeRule{{Weekdays: []int{int(time.Tuesday)}, Start: "10:30", End: "00:00"}},
		Components: []appoffers.Component{{CanonicalProductID: "burger", Quantity: 1}, {CanonicalProductID: "wings", Quantity: 1}},
	}
	if err := offers.Create(ctx, offer); err != nil {
		t.Fatal(err)
	}
	if err := offers.Publish(ctx, offer); err != nil {
		t.Fatal(err)
	}
	products := localProducts{
		{ID: "burger", Name: "麦辣鸡腿汉堡", Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeStandalone, product.SalesScopeOfferComponent}},
		{ID: "wings", Name: "麦辣鸡翅", Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeStandalone, product.SalesScopeOfferComponent}},
	}
	for _, tc := range []struct {
		name, target string
		want         bool
	}{
		{"current UTC clock", "", true},
		{"explicit UTC", "2026-09-29T04:00:00Z", true},
		{"explicit Beijing", "2026-09-29T12:00:00+08:00", true},
		{"explicit negative offset", "2026-09-29T00:00:00-04:00", true},
		{"before absolute validity", "2026-09-29T10:45:00+08:00", false},
		{"exclusive absolute expiry", "2026-09-29T13:00:00+08:00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := app.NewService(memory.NewSessionRepository(), fixedID("lunch-session"), clock, time.Hour, app.WithLocalCatalogDependencies(offers, products, domain.NewEngine(), domain.ScoringPolicy{}))
			session, err := service.StartSession(ctx, app.StartSessionCommand{})
			if err != nil {
				t.Fatal(err)
			}
			requirement := domain.Requirement{MustHave: []domain.ProductRequest{{ResolvedProductID: "burger", Quantity: 1}, {ResolvedProductID: "wings", Quantity: 1}}}
			if tc.target != "" {
				target, err := time.Parse(time.RFC3339, tc.target)
				if err != nil {
					t.Fatal(err)
				}
				requirement.TargetTime = &target
			}
			if _, err := service.UpdateRequirement(ctx, session.ID, requirement); err != nil {
				t.Fatal(err)
			}
			result, err := service.GenerateRecommendations(ctx, session.ID)
			if !tc.want {
				if err != app.ErrNoCandidates {
					t.Fatalf("expected no candidates outside validity, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Recommendations) != 1 || result.Recommendations[0].Candidate.TotalPrice.Amount != 2000 {
				t.Fatalf("unexpected recommendation: %+v", result)
			}
		})
	}
}
