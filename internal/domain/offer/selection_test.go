package offer

import (
	"math"
	"testing"
)

func TestSelectionRulesAndPricing(t *testing.T) {
	options := []Component{{CanonicalProductID: "1", Quantity: 2, ChoiceGroupID: "g", ExtraPrice: 100, ExtraPriceMode: "per_unit", ExclusiveKey: "fries"}, {CanonicalProductID: "2", Quantity: 1, ChoiceGroupID: "g", ExtraPrice: 300, ExclusiveKey: "fries"}, {CanonicalProductID: "3", Quantity: 1, ChoiceGroupID: "g"}, {CanonicalProductID: "4", Quantity: 1, ChoiceGroupID: "g"}}
	g := []ChoiceGroup{{ID: "g", MinSelect: 2, MaxSelect: 2}}
	v, err := Expand(g, options, 1000)
	if err != nil || len(v) != 5 {
		t.Fatalf("exclusive 4 choose 2: %d %v", len(v), err)
	}
	g[0].AllowRepeat = true
	options[0].MaxRepeat = 1
	v, err = Expand(g, options, 1000)
	if err != nil || len(v) != 8 {
		t.Fatalf("repeat with cap: %d %v", len(v), err)
	}
	for _, v := range v {
		expected := int64(0)
		for _, c := range v.Components {
			if c.CanonicalProductID == "1" {
				expected += 200
			}
			if c.CanonicalProductID == "2" {
				expected += 300
			}
		}
		if v.ExtraPrice != expected {
			t.Fatalf("price %d want %d", v.ExtraPrice, expected)
		}
	}
	options = append(options, options[0])
	if _, err := Expand(g, options, 1000); err == nil {
		t.Fatal("duplicate product accepted")
	}
}

func TestLimitsAndLegacy(t *testing.T) {
	c := []Component{{CanonicalProductID: "1", Quantity: 2, ChoiceGroupID: "old", ExtraPrice: math.MaxInt64}}
	v, err := Expand(nil, c, 1000)
	if err != nil || v[0].ExtraPrice != 0 {
		t.Fatalf("legacy: %v %v", v, err)
	}
	c[0].ChoiceGroupID = ""
	c[0].ExtraPriceMode = "per_unit"
	if _, err := Expand(nil, c, 0); err == nil {
		t.Fatal("overflow accepted")
	}
	options := []Component{}
	for i := 0; i < 20; i++ {
		options = append(options, Component{CanonicalProductID: string(rune('a' + i)), Quantity: 1, ChoiceGroupID: "g"})
	}
	if _, err := Expand([]ChoiceGroup{{ID: "g", MinSelect: 10, MaxSelect: 10}}, options, 0); err == nil {
		t.Fatal("variant limit accepted")
	}
	if _, err := Expand([]ChoiceGroup{{ID: "g", MinSelect: 101, MaxSelect: 101, AllowRepeat: true}}, options, 0); err == nil {
		t.Fatal("selection limit accepted")
	}
}

func TestExactFiveChooseTwo(t *testing.T) {
	options := []Component{}
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		options = append(options, Component{CanonicalProductID: id, Quantity: 1, ChoiceGroupID: "g"})
	}
	groups := []ChoiceGroup{{ID: "g", MinSelect: 2, MaxSelect: 2}}
	variants, err := Expand(groups, options, 0)
	if err != nil || len(variants) != 10 {
		t.Fatalf("5 choose 2: %d %v", len(variants), err)
	}
	groups[0].AllowRepeat = true
	variants, err = Expand(groups, options, 0)
	if err != nil || len(variants) != 15 {
		t.Fatalf("repeat 5 choose 2: %d %v", len(variants), err)
	}
}
