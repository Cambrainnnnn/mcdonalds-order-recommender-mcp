// Package placeholder supplies explicit no-data adapters so the recommendation
// pipeline can be wired before real catalog and pricing integrations exist.
package placeholder

import (
	"context"
	"errors"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

var ErrOfficialPricingNotConfigured = errors.New("official pricing adapter is not configured")

type OfferCatalog struct{}

func (OfferCatalog) ListEligible(context.Context, domain.Requirement) ([]app.OfferVersion, error) {
	return nil, nil
}

type OfficialCatalog struct{}

func (OfficialCatalog) LoadSupply(context.Context, domain.Requirement) (app.OfficialSupply, error) {
	return app.OfficialSupply{}, nil
}

type OfficialPricing struct{}

func (OfficialPricing) Calculate(context.Context, app.OfficialPriceRequest) (domain.PriceQuote, error) {
	return domain.PriceQuote{}, ErrOfficialPricingNotConfigured
}

var _ app.OfferCatalog = OfferCatalog{}
var _ app.OfficialCatalog = OfficialCatalog{}
var _ app.OfficialPricing = OfficialPricing{}
