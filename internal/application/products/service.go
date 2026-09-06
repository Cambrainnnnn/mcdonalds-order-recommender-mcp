package products

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	domain "meal-recommendation/internal/domain/product"
)

var (
	ErrNotFound = errors.New("product not found")
	ErrConflict = errors.New("product id or brand/name/specification already exists")
)

type Fields struct {
	Brand         string              `json:"brand"`
	Name          string              `json:"name"`
	Specification string              `json:"specification"`
	Category      string              `json:"category"`
	Aliases       []string            `json:"aliases"`
	Description   string              `json:"description"`
	Price         *domain.Money       `json:"price"`
	SalesScopes   []domain.SalesScope `json:"salesScopes"`
	Status        domain.Status       `json:"status"`
}

type CreateCommand struct {
	// ID must be omitted; all product IDs are allocated by the server.
	ID string `json:"id"`
	Fields
}

type Filter struct {
	Brand    string
	Category string
	Status   domain.Status
	Query    string
}

type Repository interface {
	Create(context.Context, *domain.Product) error
	Get(context.Context, string) (*domain.Product, error)
	List(context.Context) ([]*domain.Product, error)
	Save(context.Context, *domain.Product) error
}

type IDGenerator interface{ NewID() (string, error) }

type Service struct {
	repository Repository
	ids        IDGenerator
	now        func() time.Time
}

func NewService(repository Repository, ids IDGenerator, now func() time.Time) *Service {
	if repository == nil || ids == nil || now == nil {
		panic("products: repository, ID generator and clock are required")
	}
	return &Service{repository: repository, ids: ids, now: now}
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (*domain.Product, error) {
	if command.ID != "" {
		return nil, fmt.Errorf("%w: id is server-generated and must be omitted", domain.ErrInvalid)
	}
	id, err := s.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate product id: %w", err)
	}
	if command.Status == "" {
		command.Status = domain.StatusActive
	}
	if command.SalesScopes == nil {
		command.SalesScopes = []domain.SalesScope{domain.SalesScopeStandalone, domain.SalesScopeOfferComponent}
	}
	now := s.now().UTC()
	p := fromFields(id, command.Fields, now, now)
	if err := p.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	if err := s.repository.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return p, nil
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Product, error) {
	return s.repository.Get(ctx, strings.ToLower(strings.TrimSpace(id)))
}

// Update replaces all editable fields; ID and CreatedAt remain stable.
func (s *Service) Update(ctx context.Context, id string, fields Fields) (*domain.Product, error) {
	previous, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if fields.Status == "" {
		fields.Status = previous.Status
	}
	if fields.SalesScopes == nil {
		fields.SalesScopes = slices.Clone(previous.SalesScopes)
	}
	p := fromFields(previous.ID, fields, previous.CreatedAt, s.now().UTC())
	if err := p.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	if err := s.repository.Save(ctx, p); err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	return p, nil
}

// List returns every matching product without implicit pagination or limits.
func (s *Service) List(ctx context.Context, filter Filter) ([]*domain.Product, error) {
	if filter.Status != "" && filter.Status != domain.StatusActive && filter.Status != domain.StatusDisabled {
		return nil, fmt.Errorf("%w: status must be active or disabled", domain.ErrInvalid)
	}
	products, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	result := make([]*domain.Product, 0, len(products))
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, p := range products {
		if filter.Brand != "" && !strings.EqualFold(strings.TrimSpace(filter.Brand), p.Brand) {
			continue
		}
		if filter.Category != "" && !strings.EqualFold(strings.TrimSpace(filter.Category), p.Category) {
			continue
		}
		if filter.Status != "" && filter.Status != p.Status {
			continue
		}
		searchable := append([]string{p.ID, p.Name, p.Specification}, p.Aliases...)
		if query != "" && !slices.ContainsFunc(searchable, func(value string) bool { return strings.Contains(strings.ToLower(value), query) }) {
			continue
		}
		result = append(result, p)
	}
	slices.SortFunc(result, func(a, b *domain.Product) int {
		for _, pair := range [][2]string{{a.Brand, b.Brand}, {a.Name, b.Name}, {a.Specification, b.Specification}, {a.ID, b.ID}} {
			if order := strings.Compare(pair[0], pair[1]); order != 0 {
				return order
			}
		}
		return 0
	})
	return result, nil
}

func fromFields(id string, f Fields, createdAt, updatedAt time.Time) *domain.Product {
	var price *domain.Money
	if f.Price != nil {
		copy := *f.Price
		price = &copy
	}
	return &domain.Product{ID: id, Brand: f.Brand, Name: f.Name, Specification: f.Specification, Category: f.Category,
		Aliases: slices.Clone(f.Aliases), Description: f.Description, Price: price, SalesScopes: slices.Clone(f.SalesScopes), Status: f.Status, CreatedAt: createdAt, UpdatedAt: updatedAt}
}
