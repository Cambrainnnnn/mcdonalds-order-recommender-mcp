package product

import (
	"errors"
	"fmt"
	"meal-recommendation/internal/domain/identity"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid product")

type Status string

type SalesScope string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"

	SalesScopeStandalone     SalesScope = "standalone"
	SalesScopeOfferComponent SalesScope = "offer_component"
)

type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// Product identifies one brand's single item in one specification. Price and
// sales scopes describe how it may be sold, but are not part of its identity.
type Product struct {
	ID            string       `json:"id"`
	Brand         string       `json:"brand"`
	Name          string       `json:"name"`
	Specification string       `json:"specification"`
	Category      string       `json:"category,omitempty"`
	Aliases       []string     `json:"aliases"`
	Description   string       `json:"description,omitempty"`
	Price         *Money       `json:"price,omitempty"`
	SalesScopes   []SalesScope `json:"salesScopes"`
	Status        Status       `json:"status"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

func (p *Product) NormalizeAndValidate() error {
	p.ID = strings.ToLower(strings.TrimSpace(p.ID))
	p.Brand = strings.ToLower(strings.TrimSpace(p.Brand))
	p.Name = strings.TrimSpace(p.Name)
	p.Specification = strings.TrimSpace(p.Specification)
	p.Category = strings.TrimSpace(p.Category)
	p.Description = strings.TrimSpace(p.Description)
	if p.Price != nil {
		p.Price.Currency = strings.ToUpper(strings.TrimSpace(p.Price.Currency))
		if p.Price.Amount < 0 || p.Price.Currency == "" || utf8.RuneCountInString(p.Price.Currency) > 16 {
			return fmt.Errorf("%w: price amount must be non-negative and currency is required", ErrInvalid)
		}
	}
	for _, value := range append([]string{p.Brand, p.Name, p.Specification, p.Category, p.Description}, p.Aliases...) {
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%w: text must not contain null characters", ErrInvalid)
		}
	}
	if !identity.Valid(p.ID) {
		return fmt.Errorf("%w: id must be a positive 64-bit decimal snowflake string", ErrInvalid)
	}
	for _, field := range []struct {
		name, value string
		limit       int
	}{
		{"brand", p.Brand, 64}, {"name", p.Name, 200}, {"specification", p.Specification, 100},
	} {
		if field.value == "" || utf8.RuneCountInString(field.value) > field.limit {
			return fmt.Errorf("%w: %s is required and must not exceed %d characters", ErrInvalid, field.name, field.limit)
		}
	}
	if utf8.RuneCountInString(p.Category) > 100 || utf8.RuneCountInString(p.Description) > 2000 || len(p.Aliases) > 50 {
		return fmt.Errorf("%w: category, description or aliases exceeds its limit", ErrInvalid)
	}
	if p.Status != StatusActive && p.Status != StatusDisabled {
		return fmt.Errorf("%w: status must be active or disabled", ErrInvalid)
	}
	if len(p.SalesScopes) == 0 {
		return fmt.Errorf("%w: at least one sales scope is required", ErrInvalid)
	}
	scopes := make([]SalesScope, 0, len(p.SalesScopes))
	seenScopes := make(map[SalesScope]bool)
	for _, scope := range p.SalesScopes {
		if scope != SalesScopeStandalone && scope != SalesScopeOfferComponent {
			return fmt.Errorf("%w: salesScopes must contain only standalone or offer_component", ErrInvalid)
		}
		if !seenScopes[scope] {
			scopes = append(scopes, scope)
			seenScopes[scope] = true
		}
	}
	p.SalesScopes = scopes
	aliases := make([]string, 0, len(p.Aliases))
	seen := make(map[string]bool)
	for _, alias := range p.Aliases {
		alias = strings.TrimSpace(alias)
		if utf8.RuneCountInString(alias) > 200 {
			return fmt.Errorf("%w: alias must not exceed 200 characters", ErrInvalid)
		}
		key := strings.ToLower(alias)
		if alias != "" && !seen[key] {
			aliases = append(aliases, alias)
			seen[key] = true
		}
	}
	p.Aliases = aliases
	return nil
}

func (p *Product) Clone() *Product {
	copy := *p
	copy.Aliases = slices.Clone(p.Aliases)
	copy.SalesScopes = slices.Clone(p.SalesScopes)
	if p.Price != nil {
		price := *p.Price
		copy.Price = &price
	}
	return &copy
}

func (p *Product) Supports(scope SalesScope) bool {
	return slices.Contains(p.SalesScopes, scope)
}

func (p *Product) IdentityKey() string {
	return strings.ToLower(strings.Join([]string{p.Brand, p.Name, p.Specification}, "\x00"))
}
