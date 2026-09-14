// Package offer owns the selection rules shared by draft validation and pricing.
package offer

import (
	"fmt"
	"math"
	"slices"
)

const MaxVariants = 10_000
const MaxComponents = 100
const MaxSelections = 100

type Component struct {
	CanonicalProductID string `json:"canonicalProductId"`
	Quantity           int    `json:"quantity"`
	ChoiceGroupID      string `json:"choiceGroupId,omitempty"`
	ExtraPrice         int64  `json:"extraPrice,omitempty"`
	ExtraPriceMode     string `json:"extraPriceMode,omitempty"`
	MaxRepeat          int    `json:"maxRepeat,omitempty"`
	// Options with the same key represent mutually exclusive specifications.
	ExclusiveKey string `json:"exclusiveKey,omitempty"`
}

type ChoiceGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	MinSelect   int    `json:"minSelect"`
	MaxSelect   int    `json:"maxSelect"`
	AllowRepeat bool   `json:"allowRepeat,omitempty"`
}

type Variant struct {
	Components []Component
	ExtraPrice int64
}

// Expand bounds both search work and output. Legacy snapshots without groups
// retain grouped components as fixed items without their historical surcharge.
func Expand(groups []ChoiceGroup, components []Component, basePrice int64) ([]Variant, error) {
	if basePrice < 0 || len(components) == 0 || len(components) > MaxComponents || len(groups) > MaxComponents {
		return nil, fmt.Errorf("invalid price or component count (1..%d)", MaxComponents)
	}
	byGroup := map[string][]Component{}
	known := map[string]bool{}
	for _, g := range groups {
		if g.ID == "" || known[g.ID] || g.MinSelect < 0 || g.MaxSelect < 1 || g.MinSelect > g.MaxSelect || g.MaxSelect > MaxSelections {
			return nil, fmt.Errorf("invalid choice group %q", g.ID)
		}
		known[g.ID] = true
	}
	fixed := Variant{}
	seen := map[string]bool{}
	for _, c := range components {
		if c.CanonicalProductID == "" || c.Quantity < 1 || c.Quantity > math.MaxInt/MaxSelections || c.ExtraPrice < 0 || c.MaxRepeat < 0 || c.MaxRepeat > MaxSelections {
			return nil, fmt.Errorf("invalid component")
		}
		if c.ExtraPriceMode != "" && c.ExtraPriceMode != "per_selection" && c.ExtraPriceMode != "per_unit" {
			return nil, fmt.Errorf("invalid extraPriceMode")
		}
		if len(groups) > 0 && c.ChoiceGroupID != "" {
			if !known[c.ChoiceGroupID] {
				return nil, fmt.Errorf("unknown choice group %q", c.ChoiceGroupID)
			}
			key := c.ChoiceGroupID + "\x00" + c.CanonicalProductID
			if seen[key] {
				return nil, fmt.Errorf("duplicate product in choice group")
			}
			seen[key] = true
			byGroup[c.ChoiceGroupID] = append(byGroup[c.ChoiceGroupID], c)
		} else {
			if c.MaxRepeat != 0 || c.ExclusiveKey != "" {
				return nil, fmt.Errorf("repeat and exclusivity require a choice group")
			}
			charge := len(groups) > 0 || c.ChoiceGroupID == ""
			if err := appendComponent(&fixed, c, charge, basePrice); err != nil {
				return nil, err
			}
		}
	}
	selectionBudget := len(fixed.Components)
	for _, g := range groups {
		selectionBudget += g.MaxSelect
	}
	if selectionBudget > MaxSelections {
		return nil, fmt.Errorf("offer exceeds %d total selections", MaxSelections)
	}
	variants := []Variant{fixed}
	work := 0
	for _, g := range groups {
		options := byGroup[g.ID]
		capacity := 0
		caps := make([]int, len(options))
		for i, c := range options {
			cap := 1
			if g.AllowRepeat {
				cap = g.MaxSelect
				if c.MaxRepeat > 0 {
					cap = min(cap, c.MaxRepeat)
				}
			} else if c.MaxRepeat > 1 {
				return nil, fmt.Errorf("maxRepeat requires allowRepeat")
			}
			caps[i] = cap
			capacity += cap
		}
		if g.MaxSelect > capacity {
			return nil, fmt.Errorf("choice group maxSelect exceeds capacity")
		}
		selections := []Variant{}
		var walk func(int, int, Variant) error
		walk = func(start, remaining int, current Variant) error {
			work++
			if work > 1_000_000 {
				return fmt.Errorf("choice search exceeds work limit")
			}
			if remaining == 0 {
				selections = append(selections, Variant{Components: slices.Clone(current.Components), ExtraPrice: current.ExtraPrice})
				if len(selections) > MaxVariants/len(variants) {
					return fmt.Errorf("more than %d combinations", MaxVariants)
				}
				return nil
			}
			for i := start; i < len(options); i++ {
				c := options[i]
				if caps[i] == 0 {
					continue
				}
				next := Variant{Components: slices.Clone(current.Components), ExtraPrice: current.ExtraPrice}
				if err := appendComponent(&next, c, true, basePrice); err != nil {
					return err
				}
				caps[i]--
				// The same option can repeat; other specifications sharing its key cannot.
				blocked := []int{}
				if c.ExclusiveKey != "" {
					for j := i + 1; j < len(options); j++ {
						if options[j].ExclusiveKey == c.ExclusiveKey && caps[j] > 0 {
							blocked = append(blocked, j, caps[j])
							caps[j] = 0
						}
					}
				}
				if err := walk(i, remaining-1, next); err != nil {
					return err
				}
				for j := 0; j < len(blocked); j += 2 {
					caps[blocked[j]] = blocked[j+1]
				}
				caps[i]++
			}
			return nil
		}
		for n := g.MinSelect; n <= g.MaxSelect; n++ {
			if err := walk(0, n, Variant{}); err != nil {
				return nil, err
			}
		}
		if len(selections) == 0 {
			return nil, fmt.Errorf("choice group has no legal selections")
		}
		next := make([]Variant, 0, len(variants)*len(selections))
		for _, v := range variants {
			for _, selection := range selections {
				combined := Variant{Components: slices.Clone(v.Components), ExtraPrice: v.ExtraPrice}
				for _, c := range selection.Components {
					if err := appendComponent(&combined, c, true, basePrice); err != nil {
						return nil, err
					}
				}
				next = append(next, combined)
			}
		}
		variants = next
	}
	return variants, nil
}

func appendComponent(v *Variant, c Component, charge bool, base int64) error {
	totalQuantity := 0
	for _, existing := range v.Components {
		if existing.CanonicalProductID == c.CanonicalProductID {
			totalQuantity += existing.Quantity
		}
	}
	if totalQuantity > math.MaxInt-c.Quantity {
		return fmt.Errorf("product quantity overflows")
	}

	amount := c.ExtraPrice
	if !charge {
		amount = 0
	} else if c.ExtraPriceMode == "per_unit" {
		if amount > math.MaxInt64/int64(c.Quantity) {
			return fmt.Errorf("surcharge overflows")
		}
		amount *= int64(c.Quantity)
	}
	if amount > math.MaxInt64-base-v.ExtraPrice {
		return fmt.Errorf("offer price overflows")
	}
	v.ExtraPrice += amount
	c.ExtraPrice = amount
	c.ExtraPriceMode = "per_selection"
	v.Components = append(v.Components, c)
	return nil
}
