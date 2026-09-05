package recommendation_test

import (
	"context"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type fixedID string

func (id fixedID) NewID() (string, error) { return string(id), nil }

type staticOffers []app.OfferVersion

func (offers staticOffers) ListEligible(context.Context, domain.Requirement) ([]app.OfferVersion, error) {
	return offers, nil
}

type staticOfficial app.OfficialSupply

func (supply staticOfficial) LoadSupply(context.Context, domain.Requirement) (app.OfficialSupply, error) {
	return app.OfficialSupply(supply), nil
}

type staticPricing map[string]int64

func (prices staticPricing) Calculate(_ context.Context, request app.OfficialPriceRequest) (domain.PriceQuote, error) {
	amount := int64(0)
	for _, item := range request.Items {
		amount += prices[item.ProductCode] * int64(item.Quantity)
	}
	return domain.PriceQuote{
		Amount: domain.Money{Amount: amount, Currency: "CNY"}, Confidence: domain.PriceLive,
	}, nil
}

func TestServiceStartsAndLoadsSession(t *testing.T) {
	repository := memory.NewSessionRepository()
	now := time.Date(2026, time.August, 16, 1, 0, 0, 0, time.UTC)
	service := app.NewService(repository, fixedID("session-1"), func() time.Time { return now }, 30*time.Minute)

	created, err := service.StartSession(context.Background(), app.StartSessionCommand{
		OrderMode:    domain.OrderModePickup,
		City:         " 上海市 ",
		StoreKeyword: " 中山公园 ",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if created.ID != "session-1" || created.Requirement.Location.City != "上海市" {
		t.Fatalf("created session = %#v", created)
	}

	loaded, err := service.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if loaded.ID != created.ID {
		t.Fatalf("loaded ID = %q, want %q", loaded.ID, created.ID)
	}
}

func TestServiceGeneratesRecommendationsAndCompletesSession(t *testing.T) {
	repository := memory.NewSessionRepository()
	now := time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)
	service := app.NewService(
		repository,
		fixedID("session-1"),
		func() time.Time { return now },
		30*time.Minute,
		app.WithRecommendationDependencies(
			staticOffers{{
				ID: "offer-1", Source: "douyin",
				Price:       domain.PriceQuote{Amount: domain.Money{Amount: 2000, Currency: "CNY"}, Confidence: domain.PriceVerifiedToday},
				Components:  []app.OfferComponent{{CanonicalProductID: "burger", Quantity: 1}},
				Eligibility: domain.EligibilityEligible,
			}},
			staticOfficial(app.OfficialSupply{
				StoreCode: "store-1",
				Products: []app.OfficialProduct{
					{Code: "B1", CanonicalProductID: "burger", Available: true},
					{Code: "I1", CanonicalProductID: "ice-cream", Available: true},
				},
			}),
			staticPricing{"B1": 2500, "I1": 1000},
			domain.NewEngine(),
			domain.ScoringPolicy{Version: "test"},
		),
	)
	created, err := service.StartSession(context.Background(), app.StartSessionCommand{})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	_, err = service.UpdateRequirement(context.Background(), created.ID, domain.Requirement{
		OrderMode: domain.OrderModePickup,
		Location:  domain.Location{StoreCode: "store-1"},
		MustHave: []domain.ProductRequest{
			{ResolvedProductID: "burger", Quantity: 1},
			{ResolvedProductID: "ice-cream", Quantity: 1},
		},
		Budget: &domain.Money{Amount: 4000, Currency: "CNY"},
	})
	if err != nil {
		t.Fatalf("UpdateRequirement() error = %v", err)
	}

	response, err := service.GenerateRecommendations(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GenerateRecommendations() error = %v", err)
	}
	if response.Status != string(domain.SessionCompleted) || len(response.Recommendations) == 0 {
		t.Fatalf("response = %#v, want completed recommendations", response)
	}
	if response.Recommendations[0].Candidate.TotalPrice.Amount != 3000 {
		t.Fatalf("lowest price = %d, want 3000", response.Recommendations[0].Candidate.TotalPrice.Amount)
	}
	loaded, err := service.GetSession(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if loaded.State != domain.SessionCompleted {
		t.Fatalf("session state = %q, want %q", loaded.State, domain.SessionCompleted)
	}
}
