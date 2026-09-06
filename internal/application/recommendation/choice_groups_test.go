package recommendation

import (
	"testing"

	domain "meal-recommendation/internal/domain/recommendation"
)

func TestNormalizeOfferVariantsExpandsNOfMAndAddsSelectedSurcharges(t *testing.T) {
	offer := OfferVersion{
		ID: "offer-version-1", Source: "meituan",
		Price:        domain.PriceQuote{Amount: domain.Money{Amount: 1000, Currency: "CNY"}},
		ChoiceGroups: []OfferChoiceGroup{{ID: "group-1", Name: "小食三选二", MinSelect: 2, MaxSelect: 2}},
		Components: []OfferComponent{
			{CanonicalProductID: "fixed", Quantity: 1},
			{CanonicalProductID: "a", Quantity: 1, ChoiceGroupID: "group-1"},
			{CanonicalProductID: "b", Quantity: 1, ChoiceGroupID: "group-1", ExtraPrice: 100},
			{CanonicalProductID: "c", Quantity: 1, ChoiceGroupID: "group-1", ExtraPrice: 200},
		},
	}

	variants, err := normalizeOfferVariants(offer)
	if err != nil {
		t.Fatalf("normalizeOfferVariants() error = %v", err)
	}
	if len(variants) != 3 {
		t.Fatalf("variants=%d, want C(3,2)=3", len(variants))
	}
	wantPrices := []int64{1100, 1200, 1300}
	for index, variant := range variants {
		if len(variant.Products) != 3 {
			t.Fatalf("variant %d products=%#v, want one fixed and two selected", index, variant.Products)
		}
		if variant.Price.Amount.Amount != wantPrices[index] {
			t.Fatalf("variant %d price=%d, want %d", index, variant.Price.Amount.Amount, wantPrices[index])
		}
	}
	if variants[0].Products[2].ChoiceGroupID != "group-1" || variants[0].Products[2].ExtraPrice != 100 {
		t.Fatalf("selected choice metadata=%#v", variants[0].Products[2])
	}
}

func TestNormalizeOfferVariantsBuildsCartesianProductAcrossGroups(t *testing.T) {
	offer := OfferVersion{
		ID: "offer-version-2", Price: domain.PriceQuote{Amount: domain.Money{Amount: 2000, Currency: "CNY"}},
		ChoiceGroups: []OfferChoiceGroup{
			{ID: "food", MinSelect: 1, MaxSelect: 1},
			{ID: "drink", MinSelect: 1, MaxSelect: 1},
		},
		Components: []OfferComponent{
			{CanonicalProductID: "burger", Quantity: 1, ChoiceGroupID: "food"},
			{CanonicalProductID: "wrap", Quantity: 1, ChoiceGroupID: "food"},
			{CanonicalProductID: "cola", Quantity: 1, ChoiceGroupID: "drink"},
			{CanonicalProductID: "coffee", Quantity: 1, ChoiceGroupID: "drink", ExtraPrice: 300},
		},
	}

	variants, err := normalizeOfferVariants(offer)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 4 {
		t.Fatalf("variants=%d, want 2*2=4", len(variants))
	}
	if variants[1].Price.Amount.Amount != 2300 || variants[3].Price.Amount.Amount != 2300 {
		t.Fatalf("premium variants not surcharged: %#v", variants)
	}
}

func TestNormalizeOfferVariantsPreservesLegacyGroupedComponents(t *testing.T) {
	variants, err := normalizeOfferVariants(OfferVersion{
		ID: "legacy", Price: domain.PriceQuote{Amount: domain.Money{Amount: 1000, Currency: "CNY"}},
		Components: []OfferComponent{{CanonicalProductID: "burger", Quantity: 1, ChoiceGroupID: "old-group", ExtraPrice: 300}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 1 || len(variants[0].Products) != 1 || variants[0].Price.Amount.Amount != 1000 {
		t.Fatalf("variants=%#v, want legacy component kept fixed without newly charging its old surcharge", variants)
	}
}
