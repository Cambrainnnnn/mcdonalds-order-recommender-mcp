package recommendation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	productdomain "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

var (
	ErrSessionExpired            = errors.New("recommendation session has expired")
	ErrRecommendationUnavailable = errors.New("recommendation dependencies are not configured")
	ErrNoCandidates              = errors.New("no eligible recommendation candidates")
)

type IncompleteRequirementError struct {
	Fields []string
}

func (e *IncompleteRequirementError) Error() string {
	return "requirement is incomplete: " + strings.Join(e.Fields, ", ")
}

type RecommendationResponse struct {
	SessionID       string          `json:"sessionId"`
	Status          string          `json:"status"`
	Recommendations []domain.Result `json:"recommendations"`
}

func (s *Service) GenerateRecommendations(ctx context.Context, id string) (*RecommendationResponse, error) {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if session.State == domain.SessionExpired {
		return nil, ErrSessionExpired
	}
	if missing := session.Requirement.MissingFields(); len(missing) > 0 {
		return nil, &IncompleteRequirementError{Fields: missing}
	}
	if s.offers == nil || s.engine == nil || (s.products == nil && (s.officialCatalog == nil || s.officialPricing == nil)) {
		return nil, ErrRecommendationUnavailable
	}

	session.State = domain.SessionComputing
	session.UpdatedAt = s.now().UTC()
	if err := s.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("mark recommendation session computing: %w", err)
	}

	requirement := session.Requirement
	if s.products != nil {
		requirement.Location = domain.Location{}
	}
	offers, err := s.offers.ListEligible(ctx, requirement)
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("load eligible offers: %w", err)
	}
	var official OfficialSupply
	if s.products == nil {
		official, err = s.officialCatalog.LoadSupply(ctx, requirement)
		if err != nil {
			s.restoreReady(ctx, session)
			return nil, fmt.Errorf("load official supply: %w", err)
		}
	}

	options, err := normalizeCatalogOptions(offers, official)
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("normalize catalog options: %w", err)
	}
	if s.products != nil {
		products, err := s.products.List(ctx)
		if err != nil {
			s.restoreReady(ctx, session)
			return nil, fmt.Errorf("load maintained products: %w", err)
		}
		for _, p := range products {
			if p.Status != productdomain.StatusActive || !p.Supports(productdomain.SalesScopeStandalone) || p.Price == nil {
				continue
			}
			options = append(options, domain.CatalogOption{
				ID: p.ID, Channel: domain.CatalogChannel, Eligibility: domain.EligibilityEligible,
				Products: []domain.CandidateProduct{{CanonicalProductID: p.ID, Quantity: 1}},
				Price:    domain.PriceQuote{Amount: domain.Money{Amount: p.Price.Amount, Currency: p.Price.Currency}, Confidence: domain.PriceUnverified, QuotedAt: p.UpdatedAt, Source: domain.CatalogChannel},
				Warnings: []string{"价格来自服务端维护的单品参考价"},
			})
		}
	}
	generated, err := s.engine.Generate(domain.EngineInput{
		Requirement: session.Requirement,
		Options:     options,
	})
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("generate recommendation candidates: %w", err)
	}
	priced, pricingErrors := s.priceOfficialCandidates(ctx, session.Requirement, official, generated)
	if len(priced) == 0 && pricingErrors > 0 {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("price official candidates: all %d pricing requests failed", pricingErrors)
	}

	results, err := s.engine.Rank(session.Requirement, priced, s.policy)
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("rank recommendation candidates: %w", err)
	}
	if len(results) == 0 {
		s.restoreReady(ctx, session)
		return nil, ErrNoCandidates
	}

	session.State = domain.SessionCompleted
	session.UpdatedAt = s.now().UTC()
	if err := s.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("complete recommendation session: %w", err)
	}
	return &RecommendationResponse{
		SessionID:       session.ID,
		Status:          string(domain.SessionCompleted),
		Recommendations: results,
	}, nil
}

func normalizeCatalogOptions(offers []OfferVersion, official OfficialSupply) ([]domain.CatalogOption, error) {
	options := make([]domain.CatalogOption, 0, len(offers)+len(official.Products))
	for _, offer := range offers {
		variants, err := normalizeOfferVariants(offer)
		if err != nil {
			return nil, err
		}
		options = append(options, variants...)
	}
	for _, product := range official.Products {
		if !product.Available || strings.TrimSpace(product.CanonicalProductID) == "" || strings.TrimSpace(product.Code) == "" {
			continue
		}
		option := domain.CatalogOption{
			ID:      product.Code,
			Channel: domain.OfficialChannel,
			Products: []domain.CandidateProduct{{
				CanonicalProductID: product.CanonicalProductID,
				ProductCode:        product.Code,
				Quantity:           1,
			}},
			Eligibility: domain.EligibilityEligible,
			Warnings:    product.Warnings,
		}
		if product.Price != nil {
			option.Price = domain.PriceQuote{Amount: *product.Price, Confidence: domain.PriceLive, QuotedAt: official.LoadedAt, Source: domain.OfficialChannel}
		}
		options = append(options, option)
	}
	return options, nil
}

const maxNormalizedOfferVariants = 10_000

type offerVariant struct {
	products   []domain.CandidateProduct
	extraPrice int64
}

func normalizeOfferVariants(offer OfferVersion) ([]domain.CatalogOption, error) {
	eligibility := offer.Eligibility
	if eligibility == "" {
		eligibility = domain.EligibilityEligible
	}
	channel := strings.TrimSpace(offer.Source)
	if channel == "" {
		channel = "manual_offer"
	}

	fixed := offerVariant{}
	groupComponents := make(map[string][]OfferComponent, len(offer.ChoiceGroups))
	for _, component := range offer.Components {
		if strings.TrimSpace(component.CanonicalProductID) == "" {
			continue
		}
		// Published legacy offers did not carry choice-group rules. Preserve their
		// previous behavior by treating every component as fixed.
		if len(offer.ChoiceGroups) == 0 || component.ChoiceGroupID == "" {
			fixed.products = append(fixed.products, candidateProduct(component))
			// A legacy grouped component has no selection rule, so its historical
			// extraPrice was never charged. Keep that behavior until the offer is
			// updated with explicit choiceGroups.
			if len(offer.ChoiceGroups) > 0 || component.ChoiceGroupID == "" {
				if err := addExtraPrice(&fixed.extraPrice, component.ExtraPrice); err != nil {
					return nil, fmt.Errorf("offer %q: %w", offer.ID, err)
				}
			}
			continue
		}
		groupComponents[component.ChoiceGroupID] = append(groupComponents[component.ChoiceGroupID], component)
	}

	variants := []offerVariant{fixed}
	for _, group := range offer.ChoiceGroups {
		components := groupComponents[group.ID]
		if group.MinSelect < 0 || group.MaxSelect <= 0 || group.MinSelect > group.MaxSelect || group.MaxSelect > len(components) {
			return nil, fmt.Errorf("offer %q has invalid choice group %q", offer.ID, group.ID)
		}
		selectionCount := boundedSelectionCount(len(components), group.MinSelect, group.MaxSelect, maxNormalizedOfferVariants)
		if selectionCount == 0 || len(variants) > maxNormalizedOfferVariants/selectionCount {
			return nil, fmt.Errorf("offer %q produces more than %d choice combinations", offer.ID, maxNormalizedOfferVariants)
		}
		selections := componentSelections(components, group.MinSelect, group.MaxSelect)
		next := make([]offerVariant, 0, len(variants)*len(selections))
		for _, variant := range variants {
			for _, selection := range selections {
				combined := offerVariant{products: slices.Clone(variant.products), extraPrice: variant.extraPrice}
				for _, component := range selection {
					combined.products = append(combined.products, candidateProduct(component))
					if err := addExtraPrice(&combined.extraPrice, component.ExtraPrice); err != nil {
						return nil, fmt.Errorf("offer %q: %w", offer.ID, err)
					}
				}
				next = append(next, combined)
			}
		}
		variants = next
	}

	result := make([]domain.CatalogOption, 0, len(variants))
	for index, variant := range variants {
		price := offer.Price
		if price.Amount.Amount < 0 {
			return nil, fmt.Errorf("offer %q has a negative base price", offer.ID)
		}
		if variant.extraPrice > math.MaxInt64-price.Amount.Amount {
			return nil, fmt.Errorf("offer %q price overflows", offer.ID)
		}
		price.Amount.Amount += variant.extraPrice
		result = append(result, domain.CatalogOption{
			ID: fmt.Sprintf("%s#%d", offer.ID, index), Channel: channel, OfferVersionID: offer.ID,
			Products: variant.products, Price: price, Eligibility: eligibility,
			PurchaseURL: offer.PurchaseURL, Warnings: slices.Clone(offer.Warnings),
		})
	}
	return result, nil
}

func candidateProduct(component OfferComponent) domain.CandidateProduct {
	return domain.CandidateProduct{
		CanonicalProductID: component.CanonicalProductID,
		Quantity:           max(1, component.Quantity),
		ChoiceGroupID:      component.ChoiceGroupID,
		ExtraPrice:         component.ExtraPrice,
	}
}

func addExtraPrice(total *int64, amount int64) error {
	if amount < 0 || *total > math.MaxInt64-amount {
		return errors.New("component extra price is invalid")
	}
	*total += amount
	return nil
}

func componentSelections(components []OfferComponent, minSelect, maxSelect int) [][]OfferComponent {
	result := make([][]OfferComponent, 0)
	for count := minSelect; count <= maxSelect; count++ {
		appendComponentSelections(components, count, 0, nil, &result)
	}
	return result
}

func boundedSelectionCount(options, minSelect, maxSelect, limit int) int {
	total := 0
	for selected := minSelect; selected <= maxSelect; selected++ {
		combination := 1
		for i := 1; i <= selected; i++ {
			factor := options - selected + i
			if combination > limit*i/factor {
				return limit + 1
			}
			combination = combination * factor / i
		}
		total += combination
		if total > limit {
			return limit + 1
		}
	}
	return total
}

func appendComponentSelections(components []OfferComponent, remaining, start int, current []OfferComponent, result *[][]OfferComponent) {
	if remaining == 0 {
		*result = append(*result, slices.Clone(current))
		return
	}
	for index := start; index <= len(components)-remaining; index++ {
		current = append(current, components[index])
		appendComponentSelections(components, remaining-1, index+1, current, result)
		current = current[:len(current)-1]
	}
}

func (s *Service) priceOfficialCandidates(
	ctx context.Context,
	requirement domain.Requirement,
	supply OfficialSupply,
	candidates []domain.Candidate,
) ([]domain.Candidate, int) {
	priced := make([]domain.Candidate, 0, len(candidates))
	pricingErrors := 0
	for _, candidate := range candidates {
		valid := true
		for legIndex := range candidate.PurchaseLegs {
			leg := &candidate.PurchaseLegs[legIndex]
			if leg.Channel != domain.OfficialChannel {
				continue
			}
			items := make([]OfficialPriceItem, 0, len(leg.Products))
			for _, product := range leg.Products {
				items = append(items, OfficialPriceItem{
					ProductCode: product.ProductCode,
					Quantity:    product.Quantity,
				})
			}
			quote, err := s.officialPricing.Calculate(ctx, OfficialPriceRequest{
				StoreCode:       supply.StoreCode,
				BECode:          supply.BECode,
				OrderMode:       requirement.OrderMode,
				ReservationDate: reservationDate(requirement),
				Items:           items,
			})
			if err != nil {
				pricingErrors++
				valid = false
				break
			}
			leg.Price = quote
		}
		if valid {
			priced = append(priced, candidate)
		}
	}
	return priced, pricingErrors
}

func reservationDate(requirement domain.Requirement) string {
	if requirement.TargetTime == nil {
		return ""
	}
	return requirement.TargetTime.Format("2006-01-02 15:04")
}

func (s *Service) restoreReady(ctx context.Context, session *domain.Session) {
	session.State = domain.SessionReady
	session.UpdatedAt = s.now().UTC()
	_ = s.sessions.Save(context.WithoutCancel(ctx), session)
}
