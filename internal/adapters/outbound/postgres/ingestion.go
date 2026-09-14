package postgres

import (
	"context"
	"database/sql"
	"errors"

	app "meal-recommendation/internal/application/offers"
	products "meal-recommendation/internal/application/products"
)

type ingestionTx struct {
	*OfferRepository
	products *ProductRepository
}

func (t *ingestionTx) Products() products.Repository { return t.products }
func (t *ingestionTx) Request(ctx context.Context, key string) (*app.RequestRecord, error) {
	var r app.RequestRecord
	err := t.tx.QueryRowContext(ctx, `SELECT payload_hash,response FROM offer_ingestion_requests WHERE request_key=$1`, key).Scan(&r.Hash, &r.Response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}
func (t *ingestionTx) SaveRequest(ctx context.Context, key string, r app.RequestRecord) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO offer_ingestion_requests (request_key,payload_hash,response) VALUES ($1,$2,$3)`, key, r.Hash, r.Response)
	return err
}
func (r *OfferRepository) Atomic(ctx context.Context, fn func(app.IngestionTransaction) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A fixed table lock order serializes aggregate writes with catalog writes.
	// Readers remain available. Unique indexes are the final integrity guard.
	if _, err = tx.ExecContext(ctx, `LOCK TABLE offers, products, offer_ingestion_requests IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	scoped := &ingestionTx{OfferRepository: &OfferRepository{db: r.db, tx: tx}, products: &ProductRepository{db: tx}}
	if err = fn(scoped); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	r.invalidate()
	return nil
}
