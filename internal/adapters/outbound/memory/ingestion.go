package memory

import (
	"context"
	"slices"

	app "meal-recommendation/internal/application/offers"
	products "meal-recommendation/internal/application/products"
)

type IngestionStore struct {
	offers   *OfferRepository
	products *ProductRepository
	requests map[string]app.RequestRecord
}

func NewIngestionStore(o *OfferRepository, p *ProductRepository) *IngestionStore {
	return &IngestionStore{o, p, make(map[string]app.RequestRecord)}
}

type ingestionTx struct {
	*OfferRepository
	products *ProductRepository
	requests map[string]app.RequestRecord
}

func (t *ingestionTx) Products() products.Repository { return t.products }
func (t *ingestionTx) Request(_ context.Context, key string) (*app.RequestRecord, error) {
	r, ok := t.requests[key]
	if !ok {
		return nil, nil
	}
	r.Response = slices.Clone(r.Response)
	return &r, nil
}
func (t *ingestionTx) SaveRequest(_ context.Context, key string, r app.RequestRecord) error {
	r.Response = slices.Clone(r.Response)
	t.requests[key] = r
	return nil
}
func (s *IngestionStore) Atomic(ctx context.Context, fn func(app.IngestionTransaction) error) error {
	s.offers.mu.Lock()
	defer s.offers.mu.Unlock()
	s.products.mu.Lock()
	defer s.products.mu.Unlock()
	o := NewOfferRepository(s.offers.now, s.offers.staleAfter)
	for id, v := range s.offers.offers {
		o.offers[id] = cloneOffer(v)
	}
	for id, vs := range s.offers.versions {
		for _, v := range vs {
			o.versions[id] = append(o.versions[id], cloneOffer(v))
		}
	}
	p := NewProductRepository()
	for id, v := range s.products.products {
		p.products[id] = v.Clone()
	}
	requests := map[string]app.RequestRecord{}
	for key, v := range s.requests {
		requests[key] = v
	}
	if err := fn(&ingestionTx{o, p, requests}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.offers.offers = o.offers
	s.offers.versions = o.versions
	s.products.products = p.products
	s.requests = requests
	return nil
}
