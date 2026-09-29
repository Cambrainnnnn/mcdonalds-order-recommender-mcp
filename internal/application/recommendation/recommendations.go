package recommendation

import (
	"context"
	"errors"
	"fmt"
	selection "meal-recommendation/internal/domain/offer"
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
		// Resolve display metadata from the complete catalog, including disabled
		// and component-only products that remain valid within published offers.
		byID := make(map[string]*productdomain.Product, len(products))
		for _, p := range products {
			byID[p.ID] = p
		}
		for i := range options {
			for j := range options[i].Products {
				item := &options[i].Products[j]
				if p := byID[item.CanonicalProductID]; p != nil {
					item.Name, item.Specification, item.Brand = p.Name, p.Specification, p.Brand
				} else {
					options[i].Warnings = append(options[i].Warnings, "套餐包含目录中不存在的单品，无法确认其名称和规格："+item.CanonicalProductID)
				}
			}
		}
		for _, p := range products {
			if p.Status != productdomain.StatusActive || !p.Supports(productdomain.SalesScopeStandalone) || p.Price == nil {
				continue
			}
			options = append(options, domain.CatalogOption{
				ID: p.ID, Channel: domain.CatalogChannel, Eligibility: domain.EligibilityEligible,
				Products: []domain.CandidateProduct{{CanonicalProductID: p.ID, Name: p.Name, Specification: p.Specification, Brand: p.Brand, Quantity: 1}},
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
				Name:               product.Name,
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

func normalizeOfferVariants(offer OfferVersion) ([]domain.CatalogOption, error) {
	eligibility := offer.Eligibility
	if eligibility == "" {
		eligibility = domain.EligibilityEligible
	}
	channel := strings.TrimSpace(offer.Source)
	if channel == "" {
		channel = "manual_offer"
	}
	variants, err := selection.Expand(offer.ChoiceGroups, offer.Components, offer.Price.Amount.Amount)
	if err != nil {
		return nil, fmt.Errorf("offer %q: %w", offer.ID, err)
	}
	groupNames := make(map[string]string, len(offer.ChoiceGroups))
	for _, group := range offer.ChoiceGroups {
		groupNames[group.ID] = group.Name
	}
	result := make([]domain.CatalogOption, 0, len(variants))
	for index, variant := range variants {
		price := offer.Price
		price.Amount.Amount += variant.ExtraPrice
		products := make([]domain.CandidateProduct, 0, len(variant.Components))
		for _, component := range variant.Components {
			products = append(products, domain.CandidateProduct{CanonicalProductID: component.CanonicalProductID, Quantity: component.Quantity, ChoiceGroupID: component.ChoiceGroupID, ChoiceGroupName: groupNames[component.ChoiceGroupID], ExtraPrice: component.ExtraPrice})
		}
		result = append(result, domain.CatalogOption{ID: fmt.Sprintf("%s#%d", offer.ID, index), Channel: channel, OfferVersionID: offer.ID, OfferTitle: offer.Title, Products: products, Price: price, Eligibility: eligibility, PurchaseURL: offer.PurchaseURL, Warnings: slices.Clone(offer.Warnings)})
	}
	return result, nil
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
