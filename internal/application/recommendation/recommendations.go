package recommendation

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if s.offers == nil || s.officialCatalog == nil || s.officialPricing == nil || s.engine == nil {
		return nil, ErrRecommendationUnavailable
	}

	session.State = domain.SessionComputing
	session.UpdatedAt = s.now().UTC()
	if err := s.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("mark recommendation session computing: %w", err)
	}

	offers, err := s.offers.ListEligible(ctx, session.Requirement)
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("load eligible offers: %w", err)
	}
	official, err := s.officialCatalog.LoadSupply(ctx, session.Requirement)
	if err != nil {
		s.restoreReady(ctx, session)
		return nil, fmt.Errorf("load official supply: %w", err)
	}

	generated, err := s.engine.Generate(domain.EngineInput{
		Requirement: session.Requirement,
		Options:     normalizeCatalogOptions(offers, official),
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

func normalizeCatalogOptions(offers []OfferVersion, official OfficialSupply) []domain.CatalogOption {
	options := make([]domain.CatalogOption, 0, len(offers)+len(official.Products))
	for _, offer := range offers {
		products := make([]domain.CandidateProduct, 0, len(offer.Components))
		for _, component := range offer.Components {
			if strings.TrimSpace(component.CanonicalProductID) == "" {
				continue
			}
			products = append(products, domain.CandidateProduct{
				CanonicalProductID: component.CanonicalProductID,
				Quantity:           max(1, component.Quantity),
			})
		}
		eligibility := offer.Eligibility
		if eligibility == "" {
			eligibility = domain.EligibilityEligible
		}
		channel := strings.TrimSpace(offer.Source)
		if channel == "" {
			channel = "manual_offer"
		}
		options = append(options, domain.CatalogOption{
			ID:             offer.ID,
			Channel:        channel,
			OfferVersionID: offer.ID,
			Products:       products,
			Price:          offer.Price,
			Eligibility:    eligibility,
			PurchaseURL:    offer.PurchaseURL,
			Warnings:       offer.Warnings,
		})
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
	return options
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
