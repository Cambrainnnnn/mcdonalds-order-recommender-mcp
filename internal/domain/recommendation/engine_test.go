package recommendation

import (
	"testing"
	"time"
)

func TestEngineGeneratesAndRanksDistinctRecommendations(t *testing.T) {
	now := time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)
	requirement := Requirement{
		OrderMode: OrderModePickup,
		Location:  Location{StoreCode: "store-1"},
		MustHave: []ProductRequest{
			{ResolvedProductID: "burger", Quantity: 1},
			{ResolvedProductID: "ice-cream", Quantity: 1},
		},
		NiceToHave: []ProductRequest{{ResolvedProductID: "fries", Quantity: 1}},
		Budget:     &Money{Amount: 4000, Currency: "CNY"},
	}
	options := []CatalogOption{
		{
			ID: "offer-1", Channel: "douyin", OfferVersionID: "offer-1",
			Products: []CandidateProduct{
				{CanonicalProductID: "burger", Quantity: 1},
				{CanonicalProductID: "fries", Quantity: 1},
			},
			Price:       PriceQuote{Amount: Money{Amount: 2800, Currency: "CNY"}, Confidence: PriceVerifiedToday, QuotedAt: now},
			Eligibility: EligibilityEligible,
		},
		officialOption("B1", "burger", 2300, now),
		officialOption("I1", "ice-cream", 1000, now),
		officialOption("F1", "fries", 800, now),
	}

	engine := NewEngine()
	candidates, err := engine.Generate(EngineInput{Requirement: requirement, Options: options})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	results, err := engine.Rank(requirement, candidates, ScoringPolicy{Version: "test"})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 distinct choices; results=%#v", len(results), results)
	}
	if results[0].Label != LabelLowestPrice || results[0].Candidate.TotalPrice.Amount != 3300 {
		t.Fatalf("lowest result = %#v, want official cart at 3300", results[0])
	}
	if results[1].Label != LabelBestMatch || results[1].Candidate.TotalPrice.Amount != 3800 {
		t.Fatalf("best-match result = %#v, want mixed cart at 3800", results[1])
	}
	if len(results[1].Candidate.PurchaseLegs) != 2 {
		t.Fatalf("best-match legs = %d, want 2", len(results[1].Candidate.PurchaseLegs))
	}
}

func TestEngineAppliesExcludeAndBudgetHardConstraints(t *testing.T) {
	requirement := Requirement{
		OrderMode: OrderModePickup,
		Location:  Location{StoreCode: "store-1"},
		MustHave:  []ProductRequest{{ResolvedProductID: "burger", Quantity: 1}},
		Exclude:   []string{"soda"},
		Budget:    &Money{Amount: 2500, Currency: "CNY"},
	}
	candidates := []Candidate{
		candidateForTest("excluded", 2000, "burger", "soda"),
		candidateForTest("over-budget", 3000, "burger"),
		candidateForTest("eligible", 2400, "burger"),
	}

	results, err := NewEngine().Rank(requirement, candidates, ScoringPolicy{})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(results) != 1 || results[0].Candidate.ID != "eligible" {
		t.Fatalf("results = %#v, want only eligible candidate", results)
	}
}

func TestEngineDoesNotDoubleCountOneProductAcrossRepeatedRequests(t *testing.T) {
	requirement := Requirement{
		OrderMode: OrderModePickup,
		Location:  Location{StoreCode: "store-1"},
		MustHave: []ProductRequest{
			{ResolvedProductID: "burger", Quantity: 1},
			{ResolvedProductID: "burger", Quantity: 1},
		},
	}
	oneBurger := candidateForTest("one-burger", 1000, "burger")

	results, err := NewEngine().Rank(requirement, []Candidate{oneBurger}, ScoringPolicy{})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want no result for insufficient quantity", results)
	}
}

func officialOption(code, canonicalID string, amount int64, now time.Time) CatalogOption {
	return CatalogOption{
		ID: code, Channel: OfficialChannel,
		Products:    []CandidateProduct{{CanonicalProductID: canonicalID, ProductCode: code, Quantity: 1}},
		Price:       PriceQuote{Amount: Money{Amount: amount, Currency: "CNY"}, Confidence: PriceLive, QuotedAt: now},
		Eligibility: EligibilityEligible,
	}
}

func candidateForTest(id string, amount int64, products ...string) Candidate {
	items := make([]CandidateProduct, 0, len(products))
	for _, product := range products {
		items = append(items, CandidateProduct{CanonicalProductID: product, Quantity: 1})
	}
	return Candidate{
		ID: id,
		PurchaseLegs: []PurchaseLeg{{
			Channel: "test", Products: items,
			Price:       PriceQuote{Amount: Money{Amount: amount, Currency: "CNY"}, Confidence: PriceVerifiedToday},
			Eligibility: EligibilityEligible,
		}},
	}
}
