package memory

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	appoffers "meal-recommendation/internal/application/offers"
	apprec "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type OfferRepository struct {
	mu         sync.RWMutex
	offers     map[string]*appoffers.Offer
	versions   map[string][]*appoffers.Offer
	now        func() time.Time
	staleAfter time.Duration
}

func NewOfferRepository(now func() time.Time, staleAfter time.Duration) *OfferRepository {
	return &OfferRepository{
		offers: make(map[string]*appoffers.Offer), versions: make(map[string][]*appoffers.Offer),
		now: now, staleAfter: staleAfter,
	}
}

func (r *OfferRepository) Create(_ context.Context, offer *appoffers.Offer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offers[offer.ID] = cloneOffer(offer)
	return nil
}

func (r *OfferRepository) Get(_ context.Context, id string) (*appoffers.Offer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	offer, ok := r.offers[id]
	if !ok {
		return nil, appoffers.ErrOfferNotFound
	}
	return cloneOffer(offer), nil
}

func (r *OfferRepository) List(_ context.Context) ([]*appoffers.Offer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	offers := make([]*appoffers.Offer, 0, len(r.offers))
	for _, offer := range r.offers {
		offers = append(offers, cloneOffer(offer))
	}
	return offers, nil
}

func (r *OfferRepository) Save(_ context.Context, offer *appoffers.Offer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.offers[offer.ID]; !ok {
		return appoffers.ErrOfferNotFound
	}
	r.offers[offer.ID] = cloneOffer(offer)
	return nil
}

func (r *OfferRepository) Publish(_ context.Context, offer *appoffers.Offer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.offers[offer.ID]; !ok {
		return appoffers.ErrOfferNotFound
	}
	snapshot := cloneOffer(offer)
	r.offers[offer.ID] = snapshot
	r.versions[offer.ID] = append(r.versions[offer.ID], cloneOffer(snapshot))
	return nil
}

func (r *OfferRepository) ListEligible(_ context.Context, requirement domain.Requirement) ([]apprec.OfferVersion, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := r.now()
	effectiveAt := now
	if requirement.TargetTime != nil {
		effectiveAt = *requirement.TargetTime
	}
	result := make([]apprec.OfferVersion, 0)
	for id, snapshots := range r.versions {
		current := r.offers[id]
		if current == nil || current.Status != appoffers.StatusPublished || len(snapshots) == 0 {
			continue
		}
		offer := snapshots[len(snapshots)-1]
		if !offerEligible(offer, requirement, effectiveAt) {
			continue
		}
		result = append(result, recommendationOffer(offer, r.staleAfter, now.UTC()))
	}
	return result, nil
}

func offerEligible(offer *appoffers.Offer, requirement domain.Requirement, effectiveAt time.Time) bool {
	if offer.ValidFrom != nil && effectiveAt.Before(*offer.ValidFrom) || offer.ValidUntil != nil && !effectiveAt.Before(*offer.ValidUntil) {
		return false
	}
	if !appoffers.MatchesValidTime(offer.ValidTimes, effectiveAt) {
		return false
	}
	if offer.City != "" && requirement.Location.City != "" && !strings.EqualFold(offer.City, requirement.Location.City) {
		return false
	}
	if len(offer.StoreCodes) > 0 && requirement.Location.StoreCode != "" && !slices.Contains(offer.StoreCodes, requirement.Location.StoreCode) {
		return false
	}
	if len(offer.OrderModes) > 0 && !slices.Contains(offer.OrderModes, requirement.OrderMode) {
		return false
	}
	return true
}

func recommendationOffer(offer *appoffers.Offer, staleAfter time.Duration, now time.Time) apprec.OfferVersion {
	components := make([]apprec.OfferComponent, 0, len(offer.Components))
	for _, component := range offer.Components {
		components = append(components, apprec.OfferComponent{
			CanonicalProductID: component.CanonicalProductID, Quantity: component.Quantity,
			ChoiceGroupID: component.ChoiceGroupID, ExtraPrice: component.ExtraPrice,
		})
	}
	confidence := domain.PriceVerifiedPast
	warnings := []string(nil)
	quotedAt := offer.UpdatedAt
	if offer.VerifiedAt != nil {
		quotedAt = *offer.VerifiedAt
		if staleAfter <= 0 || now.Sub(*offer.VerifiedAt) <= staleAfter {
			confidence = domain.PriceVerifiedToday
		} else {
			warnings = append(warnings, "人工套餐价格已超过复核时限")
		}
	}
	return apprec.OfferVersion{
		ID: offer.VersionID, Source: offer.Source, Title: offer.Title,
		Price:        domain.PriceQuote{Amount: offer.Price, Confidence: confidence, QuotedAt: quotedAt, Source: offer.Source},
		ChoiceGroups: recommendationChoiceGroups(offer.ChoiceGroups), Components: components,
		Eligibility: domain.EligibilityEligible, PurchaseURL: offer.PurchaseURL, Warnings: warnings,
	}
}

func recommendationChoiceGroups(groups []appoffers.ChoiceGroup) []apprec.OfferChoiceGroup {
	result := make([]apprec.OfferChoiceGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, apprec.OfferChoiceGroup{ID: group.ID, Name: group.Name, MinSelect: group.MinSelect, MaxSelect: group.MaxSelect})
	}
	return result
}

func cloneOffer(offer *appoffers.Offer) *appoffers.Offer {
	if offer == nil {
		return nil
	}
	clone := *offer
	clone.StoreCodes = slices.Clone(offer.StoreCodes)
	clone.OrderModes = slices.Clone(offer.OrderModes)
	clone.Components = slices.Clone(offer.Components)
	clone.ChoiceGroups = slices.Clone(offer.ChoiceGroups)
	clone.ValidTimes = slices.Clone(offer.ValidTimes)
	for index := range clone.ValidTimes {
		clone.ValidTimes[index].Weekdays = slices.Clone(offer.ValidTimes[index].Weekdays)
	}
	if offer.ValidFrom != nil {
		value := *offer.ValidFrom
		clone.ValidFrom = &value
	}
	if offer.ValidUntil != nil {
		value := *offer.ValidUntil
		clone.ValidUntil = &value
	}
	if offer.VerifiedAt != nil {
		value := *offer.VerifiedAt
		clone.VerifiedAt = &value
	}
	return &clone
}

var _ appoffers.Repository = (*OfferRepository)(nil)
var _ apprec.OfferCatalog = (*OfferRepository)(nil)
