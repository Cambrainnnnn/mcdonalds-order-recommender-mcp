package recommendation

import (
	"context"
	"errors"
	"time"

	selection "meal-recommendation/internal/domain/offer"
	productdomain "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

var ErrSessionNotFound = errors.New("recommendation session not found")

// SessionRepository is owned by the application layer. Database adapters must
// implement this contract without leaking persistence-specific types inward.
type SessionRepository interface {
	Create(ctx context.Context, session *domain.Session) error
	Get(ctx context.Context, id string) (*domain.Session, error)
	Save(ctx context.Context, session *domain.Session) error
}

type IDGenerator interface {
	NewID() (string, error)
}

type Clock func() time.Time

// RequirementExtractor is the replaceable LLM boundary. Implementations return
// structured data only; product resolution and business validation happen later.
type RequirementExtractor interface {
	Extract(ctx context.Context, current domain.Requirement, message string) (domain.Requirement, error)
}

// OfferCatalog is implemented by PostgreSQL or another manually maintained
// offer store. Returned offer versions must be immutable snapshots.
type OfferCatalog interface {
	ListEligible(ctx context.Context, requirement domain.Requirement) ([]OfferVersion, error)
}

// OfficialCatalog is the read-only boundary for the McDonald's MCP adapter.
type OfficialCatalog interface {
	LoadSupply(ctx context.Context, requirement domain.Requirement) (OfficialSupply, error)
}

// OfficialPricing calculates authoritative prices for shortlisted official carts.
type OfficialPricing interface {
	Calculate(ctx context.Context, request OfficialPriceRequest) (domain.PriceQuote, error)
}

// CandidateEngine is deterministic domain logic. It must not call databases,
// LLMs, HTTP services, or clocks directly.
type CandidateEngine interface {
	Generate(input domain.EngineInput) ([]domain.Candidate, error)
	Rank(requirement domain.Requirement, candidates []domain.Candidate, policy domain.ScoringPolicy) ([]domain.Result, error)
}

type OfferVersion struct {
	ID           string
	Source       string
	Title        string
	Price        domain.PriceQuote
	ChoiceGroups []OfferChoiceGroup
	Components   []OfferComponent
	Eligibility  domain.EligibilityStatus
	PurchaseURL  string
	Warnings     []string
}

type OfferChoiceGroup = selection.ChoiceGroup
type OfferComponent = selection.Component

type OfficialSupply struct {
	StoreCode string
	BECode    string
	Products  []OfficialProduct
	Coupons   []OfficialCoupon
	LoadedAt  time.Time
}

type OfficialProduct struct {
	Code               string
	Name               string
	CanonicalProductID string
	Available          bool
	Price              *domain.Money
	ChoiceGroups       []OfficialChoiceGroup
	Warnings           []string
}

type OfficialChoiceGroup struct {
	ID          string
	Name        string
	MinQuantity int
	MaxQuantity int
	Choices     []OfficialChoice
}

type OfficialChoice struct {
	Code     string
	Name     string
	Quantity int
	Price    int64
}

type OfficialCoupon struct {
	ID           string
	Code         string
	ProductCodes []string
}

type OfficialPriceRequest struct {
	StoreCode       string
	BECode          string
	OrderMode       domain.OrderMode
	ReservationDate string
	Items           []OfficialPriceItem
}

type OfficialPriceItem struct {
	ProductCode string
	Quantity    int
	CouponID    string
	CouponCode  string
	Selections  map[string]string
}

// ProductCatalog provides maintained standalone products, including their prices and status.
type ProductCatalog interface {
	List(context.Context) ([]*productdomain.Product, error)
}
