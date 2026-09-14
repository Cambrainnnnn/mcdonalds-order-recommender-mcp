package offers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	appproducts "meal-recommendation/internal/application/products"
	product "meal-recommendation/internal/domain/product"
)

// ProductInput is an explicitly confirmed catalog identity. Existing products
// are reused without changing their metadata, price, status or sales scopes.
type ProductInput struct {
	Brand         string   `json:"brand"`
	Name          string   `json:"name"`
	Specification string   `json:"specification"`
	Category      string   `json:"category,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
}

type ProductBinding struct {
	ComponentIndex int          `json:"componentIndex"`
	Product        ProductInput `json:"product"`
}

type ProductResolution struct {
	ComponentIndex int    `json:"componentIndex"`
	ProductID      string `json:"productId"`
	Created        bool   `json:"created"`
}

type IngestionResult struct {
	Offer    *Offer              `json:"offer"`
	Products []ProductResolution `json:"products"`
}

type RequestRecord struct {
	Hash     string
	Response []byte
}

// IngestionTransaction exposes only operations on a single transaction. The
// adapter must roll back every change if the callback returns an error.
type IngestionTransaction interface {
	Repository
	Products() appproducts.Repository
	Request(context.Context, string) (*RequestRecord, error)
	SaveRequest(context.Context, string, RequestRecord) error
}

type IngestionStore interface {
	Atomic(context.Context, func(IngestionTransaction) error) error
}

func WithIngestionStore(store IngestionStore) Option { return func(s *Service) { s.ingestion = store } }

func (s *Service) Ingest(ctx context.Context, id string, command CreateCommand, bindings []ProductBinding, key string) (*IngestionResult, error) {
	if s.ingestion == nil {
		return nil, fmt.Errorf("%w: aggregate ingestion is not configured", ErrInvalidOffer)
	}
	if len(key) > 200 || strings.ContainsAny(key, "\x00\r\n") || (key != "" && strings.TrimSpace(key) != key) {
		return nil, fmt.Errorf("%w: invalid Idempotency-Key", ErrInvalidOffer)
	}
	// Hash the complete input before generating IDs or resolving products. Exact
	// retries replay the original response even after subsequent edits/publishing.
	hash := digest(struct {
		ID       string
		Command  CreateCommand
		Bindings []ProductBinding
	}{id, command, bindings})
	var result *IngestionResult
	// Generate IDs outside the transaction; the database-backed generator uses
	// its own connection and must not wait for the transaction's connection.
	offerID := id
	var err error
	if offerID == "" {
		offerID, err = s.ids.NewID()
		if err != nil {
			return nil, err
		}
	}
	productIDs := make([]string, len(bindings))
	if len(bindings) > 100 {
		return nil, fmt.Errorf("%w: too many product bindings", ErrInvalidOffer)
	}
	for i := range bindings {
		productIDs[i], err = s.ids.NewID()
		if err != nil {
			return nil, err
		}
	}
	err = s.ingestion.Atomic(ctx, func(tx IngestionTransaction) error {
		if key != "" {
			record, err := tx.Request(ctx, key)
			if err != nil {
				return err
			}
			if record != nil {
				if record.Hash != hash {
					return ErrIdempotencyConflict
				}
				return json.Unmarshal(record.Response, &result)
			}
		}
		now := s.now().UTC()
		o := commandOffer(offerID, command, now)
		if id != "" {
			old, err := tx.Get(ctx, id)
			if err != nil {
				return err
			}
			if old.Status != StatusDraft && old.Status != StatusDisabled {
				return ErrInvalidTransition
			}
			o.CreatedAt = old.CreatedAt
			o.Version = old.Version
		}
		resolutions := make([]ProductResolution, 0, len(bindings))
		catalog := tx.Products()
		seen := map[int]bool{}
		for i, b := range bindings {
			if b.ComponentIndex < 0 || b.ComponentIndex >= len(o.Components) || seen[b.ComponentIndex] {
				return fmt.Errorf("%w: invalid or duplicate componentIndex", ErrInvalidOffer)
			}
			seen[b.ComponentIndex] = true
			if o.Components[b.ComponentIndex].CanonicalProductID != "" {
				return fmt.Errorf("%w: supply either canonicalProductId or product binding", ErrInvalidOffer)
			}
			p := &product.Product{ID: productIDs[i], Brand: b.Product.Brand, Name: b.Product.Name, Specification: b.Product.Specification, Category: b.Product.Category, Aliases: slices.Clone(b.Product.Aliases), Status: product.StatusActive, SalesScopes: []product.SalesScope{product.SalesScopeOfferComponent}, CreatedAt: now, UpdatedAt: now}
			if err := p.NormalizeAndValidate(); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidOffer, err)
			}
			all, err := catalog.List(ctx)
			if err != nil {
				return err
			}
			created := true
			for _, existing := range all {
				if existing.IdentityKey() == p.IdentityKey() {
					p = existing
					created = false
					break
				}
			}
			if created {
				if err := catalog.Create(ctx, p); err != nil {
					return err
				}
			}
			o.Components[b.ComponentIndex].CanonicalProductID = p.ID
			resolutions = append(resolutions, ProductResolution{ComponentIndex: b.ComponentIndex, ProductID: p.ID, Created: created})
		}
		if err := validateOffer(o); err != nil {
			return err
		}
		validator := &Service{products: catalog}
		if err := validator.validateProducts(ctx, o); err != nil {
			return err
		}
		if id == "" {
			err = tx.Create(ctx, o)
		} else {
			err = tx.Save(ctx, o)
		}
		if err != nil {
			return err
		}
		result = &IngestionResult{Offer: o, Products: resolutions}
		if key != "" {
			data, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if err = tx.SaveRequest(ctx, key, RequestRecord{Hash: hash, Response: data}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, appproducts.ErrConflict) {
			return nil, fmt.Errorf("%w: product identity conflict", ErrOfferConflict)
		}
		return nil, err
	}
	return result, nil
}
