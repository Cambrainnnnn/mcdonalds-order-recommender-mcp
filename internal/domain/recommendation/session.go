package recommendation

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidSessionID   = errors.New("session id is required")
	ErrInvalidTTL         = errors.New("session ttl must be positive")
	ErrInvalidOrderMode   = errors.New("unsupported order mode")
	ErrInvalidRequirement = errors.New("invalid dining requirement")
)

type SessionState string

const (
	SessionCollecting SessionState = "collecting"
	SessionReady      SessionState = "ready"
	SessionComputing  SessionState = "computing"
	SessionCompleted  SessionState = "completed"
	SessionExpired    SessionState = "expired"
)

type OrderMode string

const (
	OrderModePickup    OrderMode = "pickup"
	OrderModeDelivery  OrderMode = "delivery"
	OrderModeDriveThru OrderMode = "drive_thru"
)

func (m OrderMode) Valid() bool {
	return m == "" || slices.Contains([]OrderMode{OrderModePickup, OrderModeDelivery, OrderModeDriveThru}, m)
}

type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type ProductRequest struct {
	Query             string   `json:"query"`
	Quantity          int      `json:"quantity"`
	ResolvedProductID string   `json:"resolvedProductId,omitempty"`
	AllowedProductIDs []string `json:"allowedProductIds,omitempty"`
}

type Location struct {
	City         string `json:"city,omitempty"`
	StoreCode    string `json:"storeCode,omitempty"`
	StoreKeyword string `json:"storeKeyword,omitempty"`
}

type ObjectiveWeights struct {
	Price           float64 `json:"price"`
	PreferenceMatch float64 `json:"preferenceMatch"`
	AvoidWaste      float64 `json:"avoidWaste"`
	Convenience     float64 `json:"convenience"`
}

type Requirement struct {
	People     int              `json:"people,omitempty"`
	OrderMode  OrderMode        `json:"orderMode,omitempty"`
	Location   Location         `json:"location"`
	TargetTime *time.Time       `json:"targetTime,omitempty"`
	MustHave   []ProductRequest `json:"mustHave,omitempty"`
	NiceToHave []ProductRequest `json:"niceToHave,omitempty"`
	Exclude    []string         `json:"exclude,omitempty"`
	Budget     *Money           `json:"budget,omitempty"`
	Objectives ObjectiveWeights `json:"objectives"`
}

func DefaultObjectives() ObjectiveWeights {
	return ObjectiveWeights{
		Price:           0.8,
		PreferenceMatch: 1,
		AvoidWaste:      0.7,
		Convenience:     0.5,
	}
}

func (r Requirement) MissingFields() []string {
	missing := make([]string, 0, 3)
	if r.OrderMode == "" {
		missing = append(missing, "orderMode")
	}
	if strings.TrimSpace(r.Location.StoreCode) == "" && strings.TrimSpace(r.Location.StoreKeyword) == "" {
		missing = append(missing, "location")
	}
	if len(r.MustHave) == 0 {
		missing = append(missing, "mustHave")
	}
	return missing
}

func (r Requirement) NormalizeAndValidate() (Requirement, error) {
	if !r.OrderMode.Valid() {
		return Requirement{}, ErrInvalidOrderMode
	}
	if r.People < 0 {
		return Requirement{}, fmt.Errorf("%w: people cannot be negative", ErrInvalidRequirement)
	}
	r.Location.City = strings.TrimSpace(r.Location.City)
	r.Location.StoreCode = strings.TrimSpace(r.Location.StoreCode)
	r.Location.StoreKeyword = strings.TrimSpace(r.Location.StoreKeyword)

	normalizeRequests := func(requests []ProductRequest) ([]ProductRequest, error) {
		normalized := slices.Clone(requests)
		for index := range normalized {
			normalized[index].Query = strings.TrimSpace(normalized[index].Query)
			normalized[index].ResolvedProductID = strings.TrimSpace(normalized[index].ResolvedProductID)
			allowedIDs := make([]string, 0, len(normalized[index].AllowedProductIDs))
			for _, allowedID := range normalized[index].AllowedProductIDs {
				if trimmed := strings.TrimSpace(allowedID); trimmed != "" {
					allowedIDs = append(allowedIDs, trimmed)
				}
			}
			normalized[index].AllowedProductIDs = allowedIDs
			if normalized[index].Query == "" && normalized[index].ResolvedProductID == "" && len(normalized[index].AllowedProductIDs) == 0 {
				return nil, fmt.Errorf("%w: product request requires a query or product ID", ErrInvalidRequirement)
			}
			if normalized[index].Quantity <= 0 {
				normalized[index].Quantity = 1
			}
		}
		return normalized, nil
	}
	var err error
	r.MustHave, err = normalizeRequests(r.MustHave)
	if err != nil {
		return Requirement{}, err
	}
	r.NiceToHave, err = normalizeRequests(r.NiceToHave)
	if err != nil {
		return Requirement{}, err
	}
	r.Exclude = slices.Clone(r.Exclude)
	for index := range r.Exclude {
		r.Exclude[index] = strings.TrimSpace(r.Exclude[index])
	}
	r.Exclude = slices.DeleteFunc(r.Exclude, func(value string) bool { return value == "" })
	if r.Budget != nil {
		if r.Budget.Amount < 0 {
			return Requirement{}, fmt.Errorf("%w: budget cannot be negative", ErrInvalidRequirement)
		}
		budget := *r.Budget
		budget.Currency = strings.ToUpper(strings.TrimSpace(budget.Currency))
		if budget.Currency == "" {
			budget.Currency = "CNY"
		}
		r.Budget = &budget
	}
	if r.Objectives == (ObjectiveWeights{}) {
		r.Objectives = DefaultObjectives()
	}
	if r.Objectives.Price < 0 || r.Objectives.PreferenceMatch < 0 || r.Objectives.AvoidWaste < 0 || r.Objectives.Convenience < 0 {
		return Requirement{}, fmt.Errorf("%w: objective weights cannot be negative", ErrInvalidRequirement)
	}
	return r, nil
}

type Session struct {
	ID          string       `json:"id"`
	UserID      string       `json:"userId,omitempty"`
	State       SessionState `json:"state"`
	Requirement Requirement  `json:"requirement"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	ExpiresAt   time.Time    `json:"expiresAt"`
}

func NewSession(id, userID string, requirement Requirement, now time.Time, ttl time.Duration) (*Session, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalidSessionID
	}
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	requirement, err := requirement.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}

	return &Session{
		ID:          id,
		UserID:      userID,
		State:       SessionCollecting,
		Requirement: requirement,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
		ExpiresAt:   now.UTC().Add(ttl),
	}, nil
}

func (s *Session) UpdateRequirement(requirement Requirement, now time.Time) error {
	normalized, err := requirement.NormalizeAndValidate()
	if err != nil {
		return err
	}
	s.Requirement = normalized
	s.UpdatedAt = now.UTC()
	if len(normalized.MissingFields()) == 0 {
		s.State = SessionReady
	} else {
		s.State = SessionCollecting
	}
	return nil
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Requirement.MustHave = cloneProductRequests(s.Requirement.MustHave)
	clone.Requirement.NiceToHave = cloneProductRequests(s.Requirement.NiceToHave)
	clone.Requirement.Exclude = slices.Clone(s.Requirement.Exclude)
	if s.Requirement.TargetTime != nil {
		targetTime := *s.Requirement.TargetTime
		clone.Requirement.TargetTime = &targetTime
	}
	if s.Requirement.Budget != nil {
		budget := *s.Requirement.Budget
		clone.Requirement.Budget = &budget
	}
	return &clone
}

func cloneProductRequests(requests []ProductRequest) []ProductRequest {
	clones := slices.Clone(requests)
	for index := range clones {
		clones[index].AllowedProductIDs = slices.Clone(requests[index].AllowedProductIDs)
	}
	return clones
}
