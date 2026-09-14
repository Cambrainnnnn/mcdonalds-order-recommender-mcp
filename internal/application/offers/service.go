package offers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	appProducts "meal-recommendation/internal/application/products"
	"meal-recommendation/internal/domain/identity"
	selection "meal-recommendation/internal/domain/offer"
	product "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

var (
	ErrOfferConflict       = errors.New("offer already exists")
	ErrIdempotencyConflict = errors.New("idempotency key payload mismatch")
	ErrOfferNotFound       = errors.New("offer not found")
	ErrInvalidOffer        = errors.New("invalid offer")
	ErrInvalidTransition   = errors.New("invalid offer status transition")
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusReviewed  Status = "reviewed"
	StatusPublished Status = "published"
	StatusDisabled  Status = "disabled"
)

type Component = selection.Component
type ChoiceGroup = selection.ChoiceGroup

// ValidTimeRule describes a daily availability window. Weekdays use Go's
// time.Weekday numbering (Sunday=0); an empty list means every day. Start is
// inclusive and End is exclusive, both in HH:MM format.
type ValidTimeRule struct {
	Weekdays []int  `json:"weekdays,omitempty"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

type Offer struct {
	ExternalID   string             `json:"externalId,omitempty"`
	ID           string             `json:"id"`
	VersionID    string             `json:"versionId,omitempty"`
	Version      int                `json:"version"`
	Status       Status             `json:"status"`
	Source       string             `json:"source"`
	Title        string             `json:"title"`
	Price        domain.Money       `json:"price"`
	PurchaseURL  string             `json:"purchaseUrl,omitempty"`
	City         string             `json:"city,omitempty"`
	StoreCodes   []string           `json:"storeCodes,omitempty"`
	OrderModes   []domain.OrderMode `json:"orderModes,omitempty"`
	ValidFrom    *time.Time         `json:"validFrom,omitempty"`
	ValidUntil   *time.Time         `json:"validUntil,omitempty"`
	ValidTimes   []ValidTimeRule    `json:"validTimes,omitempty"`
	VerifiedAt   *time.Time         `json:"verifiedAt,omitempty"`
	ChoiceGroups []ChoiceGroup      `json:"choiceGroups,omitempty"`
	Components   []Component        `json:"components"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

type CreateCommand struct {
	ExternalID   string
	Source       string
	Title        string
	Price        domain.Money
	PurchaseURL  string
	City         string
	StoreCodes   []string
	OrderModes   []domain.OrderMode
	ValidFrom    *time.Time
	ValidUntil   *time.Time
	ValidTimes   []ValidTimeRule
	ChoiceGroups []ChoiceGroup
	Components   []Component
}

type UpdateCommand = CreateCommand

type ImportResult struct {
	Created []*Offer      `json:"created"`
	Errors  []ImportError `json:"errors,omitempty"`
}

type ImportError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

type Repository interface {
	Create(ctx context.Context, offer *Offer) error
	Get(ctx context.Context, id string) (*Offer, error)
	List(ctx context.Context) ([]*Offer, error)
	Save(ctx context.Context, offer *Offer) error
	Publish(ctx context.Context, offer *Offer) error
}

type IDGenerator interface{ NewID() (string, error) }
type Clock func() time.Time

type Service struct {
	ingestion  IngestionStore
	repository Repository
	ids        IDGenerator
	now        Clock
	products   ProductCatalog
}

type ProductCatalog interface {
	Get(context.Context, string) (*product.Product, error)
}

type Option func(*Service)

func WithProductCatalog(catalog ProductCatalog) Option {
	if catalog == nil {
		panic("offers: product catalog is required")
	}
	return func(s *Service) { s.products = catalog }
}

func NewService(repository Repository, ids IDGenerator, now Clock, options ...Option) *Service {
	if repository == nil || ids == nil || now == nil {
		panic("offers: repository, ID generator and clock are required")
	}
	service := &Service{repository: repository, ids: ids, now: now}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (*Offer, error) {
	if s.ingestion != nil {
		result, err := s.Ingest(ctx, "", command, nil, "")
		if err != nil {
			return nil, err
		}
		return result.Offer, nil
	}

	id, err := s.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate offer id: %w", err)
	}
	now := s.now().UTC()
	offer := commandOffer(id, command, now)
	if err := validateOffer(offer); err != nil {
		return nil, err
	}
	if err := s.validateProducts(ctx, offer); err != nil {
		return nil, err
	}
	if err := s.repository.Create(ctx, offer); err != nil {
		return nil, fmt.Errorf("create offer: %w", err)
	}
	return offer, nil
}

func (s *Service) List(ctx context.Context) ([]*Offer, error) {
	offers, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list offers: %w", err)
	}
	slices.SortFunc(offers, func(a, b *Offer) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return offers, nil
}

func (s *Service) UpdateDraft(ctx context.Context, id string, command UpdateCommand) (*Offer, error) {
	if s.ingestion != nil {
		result, err := s.Ingest(ctx, id, command, nil, "")
		if err != nil {
			return nil, err
		}
		return result.Offer, nil
	}

	offer, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if offer.Status != StatusDraft && offer.Status != StatusDisabled {
		return nil, fmt.Errorf("%w: only draft or disabled offers can be updated", ErrInvalidTransition)
	}
	updated := commandOffer(id, command, offer.CreatedAt)
	updated.Version = offer.Version
	updated.UpdatedAt = s.now().UTC()
	if err := validateOffer(updated); err != nil {
		return nil, err
	}
	if err := s.validateProducts(ctx, updated); err != nil {
		return nil, err
	}
	if err := s.repository.Save(ctx, updated); err != nil {
		return nil, fmt.Errorf("update offer: %w", err)
	}
	return updated, nil
}

func (s *Service) Import(ctx context.Context, commands []CreateCommand) ImportResult {
	result := ImportResult{Created: make([]*Offer, 0, len(commands))}
	for index, command := range commands {
		offer, err := s.Create(ctx, command)
		if err != nil {
			result.Errors = append(result.Errors, ImportError{Row: index + 2, Message: err.Error()})
			continue
		}
		result.Created = append(result.Created, offer)
	}
	return result
}

func (s *Service) Review(ctx context.Context, id string) (*Offer, error) {
	return s.transition(ctx, id, StatusDraft, StatusReviewed, false)
}

func (s *Service) Publish(ctx context.Context, id string) (*Offer, error) {
	return s.transition(ctx, id, StatusReviewed, StatusPublished, true)
}

func (s *Service) Verify(ctx context.Context, id string) (*Offer, error) {
	offer, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if offer.Status != StatusPublished {
		return nil, fmt.Errorf("%w: only published offers can be verified", ErrInvalidTransition)
	}
	if err := s.validateProducts(ctx, offer); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	offer.VerifiedAt = &now
	offer.UpdatedAt = now
	offer.Version++
	offer.VersionID, err = s.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate offer version id: %w", err)
	}
	if err := s.repository.Publish(ctx, offer); err != nil {
		return nil, fmt.Errorf("verify offer: %w", err)
	}
	return offer, nil
}

func (s *Service) Disable(ctx context.Context, id string) (*Offer, error) {
	offer, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if offer.Status == StatusDisabled {
		return offer, nil
	}
	offer.Status = StatusDisabled
	offer.UpdatedAt = s.now().UTC()
	if err := s.repository.Save(ctx, offer); err != nil {
		return nil, fmt.Errorf("disable offer: %w", err)
	}
	return offer, nil
}

func (s *Service) transition(ctx context.Context, id string, from, to Status, publish bool) (*Offer, error) {
	offer, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Disabled offers must be reviewed again before a new version is published.
	reopening := from == StatusDraft && to == StatusReviewed && offer.Status == StatusDisabled
	if offer.Status != from && !reopening {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrInvalidTransition, from, offer.Status)
	}
	if err := s.validateProducts(ctx, offer); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	offer.Status = to
	offer.UpdatedAt = now
	if publish {
		offer.Version++
		offer.VersionID, err = s.ids.NewID()
		if err != nil {
			return nil, fmt.Errorf("generate offer version id: %w", err)
		}
		if offer.VerifiedAt == nil {
			offer.VerifiedAt = &now
		}
		err = s.repository.Publish(ctx, offer)
	} else {
		err = s.repository.Save(ctx, offer)
	}
	if err != nil {
		return nil, fmt.Errorf("transition offer to %s: %w", to, err)
	}
	return offer, nil
}

func commandOffer(id string, command CreateCommand, createdAt time.Time) *Offer {
	return &Offer{
		ID: id, ExternalID: strings.TrimSpace(command.ExternalID), Status: StatusDraft, Source: strings.TrimSpace(command.Source), Title: strings.TrimSpace(command.Title),
		Price: command.Price, PurchaseURL: strings.TrimSpace(command.PurchaseURL), City: strings.TrimSpace(command.City),
		StoreCodes: command.StoreCodes, OrderModes: command.OrderModes, ValidFrom: command.ValidFrom,
		ValidUntil: command.ValidUntil, ValidTimes: command.ValidTimes, ChoiceGroups: slices.Clone(command.ChoiceGroups), Components: slices.Clone(command.Components), CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func (s *Service) validateProducts(ctx context.Context, offer *Offer) error {
	if s.products == nil {
		return nil
	}
	for index := range offer.Components {
		component := &offer.Components[index]
		p, err := s.products.Get(ctx, component.CanonicalProductID)
		if errors.Is(err, appProducts.ErrNotFound) {
			return fmt.Errorf("%w: component %d product %q does not exist; register it in the product catalog first", ErrInvalidOffer, index+1, component.CanonicalProductID)
		}
		if err != nil {
			return fmt.Errorf("look up component product: %w", err)
		}
		if !p.Supports(product.SalesScopeOfferComponent) {
			return fmt.Errorf("%w: component %d product %q is not available as an offer component", ErrInvalidOffer, index+1, p.ID)
		}
		component.CanonicalProductID = p.ID
	}
	return nil
}

func validateOffer(offer *Offer) error {
	offer.Price.Currency = strings.ToUpper(strings.TrimSpace(offer.Price.Currency))
	for _, field := range []struct {
		value string
		limit int
	}{{offer.Source, 64}, {offer.Title, 200}, {offer.ExternalID, 200}} {
		if strings.ContainsRune(field.value, '\x00') || utf8.RuneCountInString(field.value) > field.limit {
			return fmt.Errorf("%w: source, title or externalId is invalid", ErrInvalidOffer)
		}
	}
	if offer.Source == "" || offer.Title == "" {
		return fmt.Errorf("%w: source and title are required", ErrInvalidOffer)
	}
	if offer.Price.Amount < 0 || strings.TrimSpace(offer.Price.Currency) == "" {
		return fmt.Errorf("%w: price and currency are invalid", ErrInvalidOffer)
	}
	if len(offer.Components) == 0 {
		return fmt.Errorf("%w: at least one component is required", ErrInvalidOffer)
	}
	groups := make(map[string]ChoiceGroup, len(offer.ChoiceGroups))

	for index := range offer.ChoiceGroups {
		group := &offer.ChoiceGroups[index]
		group.ID = strings.TrimSpace(group.ID)
		group.Name = strings.TrimSpace(group.Name)
		if !identity.Valid(group.ID) {
			return fmt.Errorf("%w: choice group %d ID must be a snowflake string", ErrInvalidOffer, index+1)
		}
		if _, exists := groups[group.ID]; exists {
			return fmt.Errorf("%w: duplicate choice group ID %q", ErrInvalidOffer, group.ID)
		}
		if group.MinSelect < 0 || group.MaxSelect <= 0 || group.MinSelect > group.MaxSelect {
			return fmt.Errorf("%w: choice group %q requires 0 <= minSelect <= maxSelect and maxSelect > 0", ErrInvalidOffer, group.ID)
		}
		groups[group.ID] = *group
	}
	for index := range offer.Components {
		offer.Components[index].CanonicalProductID = strings.TrimSpace(offer.Components[index].CanonicalProductID)
		offer.Components[index].ChoiceGroupID = strings.TrimSpace(offer.Components[index].ChoiceGroupID)
		offer.Components[index].ExclusiveKey = strings.TrimSpace(offer.Components[index].ExclusiveKey)
		component := offer.Components[index]
		if component.ExtraPrice < 0 {
			return fmt.Errorf("%w: component %d extraPrice must not be negative", ErrInvalidOffer, index+1)
		}
		if component.ChoiceGroupID != "" {
			if !identity.Valid(component.ChoiceGroupID) {
				return fmt.Errorf("%w: component %d choiceGroupId must be a snowflake string", ErrInvalidOffer, index+1)
			}
			if len(groups) > 0 {
				if _, exists := groups[component.ChoiceGroupID]; !exists {
					return fmt.Errorf("%w: component %d references unknown choice group %q", ErrInvalidOffer, index+1, component.ChoiceGroupID)
				}
			}
		}
		if !identity.Valid(component.CanonicalProductID) || component.Quantity <= 0 {
			return fmt.Errorf("%w: component product ID must be a snowflake string and quantity must be positive", ErrInvalidOffer)
		}
	}
	if _, err := selection.Expand(offer.ChoiceGroups, offer.Components, offer.Price.Amount); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOffer, err)
	}
	for _, mode := range offer.OrderModes {
		if mode == "" || !mode.Valid() {
			return fmt.Errorf("%w: invalid order mode %q", ErrInvalidOffer, mode)
		}
	}
	if offer.ValidFrom != nil && offer.ValidUntil != nil && !offer.ValidUntil.After(*offer.ValidFrom) {
		return fmt.Errorf("%w: validUntil must be after validFrom", ErrInvalidOffer)
	}
	for _, rule := range offer.ValidTimes {
		start, err := time.Parse("15:04", rule.Start)
		if err != nil {
			return fmt.Errorf("%w: invalid valid time start %q", ErrInvalidOffer, rule.Start)
		}
		end, err := time.Parse("15:04", rule.End)
		if err != nil {
			return fmt.Errorf("%w: invalid valid time end %q", ErrInvalidOffer, rule.End)
		}
		if start.Equal(end) {
			return fmt.Errorf("%w: valid time start and end must differ", ErrInvalidOffer)
		}
		seen := make(map[int]struct{}, len(rule.Weekdays))
		for _, weekday := range rule.Weekdays {
			if weekday < int(time.Sunday) || weekday > int(time.Saturday) {
				return fmt.Errorf("%w: weekday must be between 0 and 6", ErrInvalidOffer)
			}
			if _, ok := seen[weekday]; ok {
				return fmt.Errorf("%w: duplicate weekday %d", ErrInvalidOffer, weekday)
			}
			seen[weekday] = struct{}{}
		}
	}
	return nil
}

func MatchesValidTime(rules []ValidTimeRule, at time.Time) bool {
	if len(rules) == 0 {
		return true
	}
	minute := at.Hour()*60 + at.Minute()
	for _, rule := range rules {
		if len(rule.Weekdays) > 0 && !slices.Contains(rule.Weekdays, int(at.Weekday())) {
			continue
		}
		start, startErr := time.Parse("15:04", rule.Start)
		end, endErr := time.Parse("15:04", rule.End)
		if startErr != nil || endErr != nil {
			continue
		}
		startMinute, endMinute := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
		if startMinute < endMinute && minute >= startMinute && minute < endMinute {
			return true
		}
		if startMinute > endMinute && (minute >= startMinute || minute < endMinute) {
			return true
		}
	}
	return false
}
