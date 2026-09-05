package recommendation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	domain "meal-recommendation/internal/domain/recommendation"
)

type StartSessionCommand struct {
	UserID       string
	OrderMode    domain.OrderMode
	City         string
	StoreCode    string
	StoreKeyword string
}

type Service struct {
	sessions        SessionRepository
	ids             IDGenerator
	now             Clock
	ttl             time.Duration
	offers          OfferCatalog
	officialCatalog OfficialCatalog
	officialPricing OfficialPricing
	engine          CandidateEngine
	policy          domain.ScoringPolicy
}

type ServiceOption func(*Service)

func WithRecommendationDependencies(
	offers OfferCatalog,
	officialCatalog OfficialCatalog,
	officialPricing OfficialPricing,
	engine CandidateEngine,
	policy domain.ScoringPolicy,
) ServiceOption {
	return func(service *Service) {
		service.offers = offers
		service.officialCatalog = officialCatalog
		service.officialPricing = officialPricing
		service.engine = engine
		service.policy = policy
	}
}

func NewService(sessions SessionRepository, ids IDGenerator, now Clock, ttl time.Duration, options ...ServiceOption) *Service {
	if sessions == nil {
		panic("recommendation: session repository is required")
	}
	if ids == nil {
		panic("recommendation: id generator is required")
	}
	if now == nil {
		panic("recommendation: clock is required")
	}
	if ttl <= 0 {
		panic("recommendation: session ttl must be positive")
	}
	service := &Service{sessions: sessions, ids: ids, now: now, ttl: ttl}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) UpdateRequirement(ctx context.Context, id string, requirement domain.Requirement) (*domain.Session, error) {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if session.State == domain.SessionExpired {
		return nil, ErrSessionExpired
	}
	if err := session.UpdateRequirement(requirement, s.now()); err != nil {
		return nil, err
	}
	if err := s.sessions.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("save recommendation requirement: %w", err)
	}
	return session.Clone(), nil
}

func (s *Service) StartSession(ctx context.Context, command StartSessionCommand) (*domain.Session, error) {
	if !command.OrderMode.Valid() {
		return nil, domain.ErrInvalidOrderMode
	}

	id, err := s.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}

	session, err := domain.NewSession(id, strings.TrimSpace(command.UserID), domain.Requirement{
		OrderMode: command.OrderMode,
		Location: domain.Location{
			City:         strings.TrimSpace(command.City),
			StoreCode:    strings.TrimSpace(command.StoreCode),
			StoreKeyword: strings.TrimSpace(command.StoreKeyword),
		},
	}, s.now(), s.ttl)
	if err != nil {
		return nil, err
	}

	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("create recommendation session: %w", err)
	}
	return session.Clone(), nil
}

func (s *Service) GetSession(ctx context.Context, id string) (*domain.Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrSessionNotFound
	}
	session, err := s.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.now().Before(session.ExpiresAt) {
		session.State = domain.SessionExpired
		if err := s.sessions.Save(ctx, session); err != nil && !errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("expire recommendation session: %w", err)
		}
	}
	return session.Clone(), nil
}
