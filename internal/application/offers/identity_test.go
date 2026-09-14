package offers

import "testing"

func TestBusinessKey(t *testing.T) {
	a := &Offer{Source: " Meituan ", Title: "Lunch  Deal", ChoiceGroups: []ChoiceGroup{{ID: "1", MinSelect: 1, MaxSelect: 1}}, Components: []Component{{CanonicalProductID: "11", Quantity: 1, ChoiceGroupID: "1"}, {CanonicalProductID: "12", Quantity: 1, ChoiceGroupID: "1"}}}
	b := *a
	b.Source = "meituan"
	b.Title = "lunch deal"
	b.ChoiceGroups = []ChoiceGroup{{ID: "2", Name: "ignored", MinSelect: 1, MaxSelect: 1}}
	b.Components = []Component{{CanonicalProductID: "12", Quantity: 1, ChoiceGroupID: "2"}, {CanonicalProductID: "11", Quantity: 1, ChoiceGroupID: "2", ExtraPriceMode: "per_selection"}}
	if BusinessKey(a) != BusinessKey(&b) {
		t.Fatal("ordering/group labels affect identity")
	}
	b.Components[0].ExtraPrice = 10
	if BusinessKey(a) == BusinessKey(&b) {
		t.Fatal("surcharge ignored")
	}
	a.ExternalID = "sku"
	b.ExternalID = "sku"
	if BusinessKey(a) != BusinessKey(&b) {
		t.Fatal("external ID not authoritative")
	}
	b.Source = "other"
	if BusinessKey(a) == BusinessKey(&b) {
		t.Fatal("channel ignored")
	}
}
