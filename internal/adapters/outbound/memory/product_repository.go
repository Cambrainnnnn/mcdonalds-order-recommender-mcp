package memory

import (
	"context"
	"sync"

	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
)

type ProductRepository struct {
	mu       sync.RWMutex
	products map[string]*domain.Product
}

func NewProductRepository() *ProductRepository {
	return &ProductRepository{products: make(map[string]*domain.Product)}
}

func (r *ProductRepository) Create(_ context.Context, p *domain.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.products[p.ID]; exists {
		return app.ErrConflict
	}
	return r.save(p)
}

func (r *ProductRepository) Save(_ context.Context, p *domain.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.products[p.ID]; !exists {
		return app.ErrNotFound
	}
	return r.save(p)
}

func (r *ProductRepository) save(p *domain.Product) error {
	for id, existing := range r.products {
		if id != p.ID && existing.IdentityKey() == p.IdentityKey() {
			return app.ErrConflict
		}
	}
	r.products[p.ID] = p.Clone()
	return nil
}

func (r *ProductRepository) Get(_ context.Context, id string) (*domain.Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, exists := r.products[id]
	if !exists {
		return nil, app.ErrNotFound
	}
	return p.Clone(), nil
}

func (r *ProductRepository) List(_ context.Context) ([]*domain.Product, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*domain.Product, 0, len(r.products))
	for _, p := range r.products {
		result = append(result, p.Clone())
	}
	return result, nil
}

var _ app.Repository = (*ProductRepository)(nil)
