package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
)

type ProductRepository struct{ db executor }

func NewProductRepository(db *sql.DB) *ProductRepository { return &ProductRepository{db: db} }

const productColumns = `id,brand,name,specification,category,aliases,description,price_amount,price_currency,sales_scopes,status,created_at,updated_at`

func (r *ProductRepository) Create(ctx context.Context, p *domain.Product) error {
	aliases, err := json.Marshal(p.Aliases)
	if err != nil {
		return err
	}
	scopes, err := json.Marshal(p.SalesScopes)
	if err != nil {
		return err
	}
	var priceAmount any
	var priceCurrency any
	if p.Price != nil {
		priceAmount, priceCurrency = p.Price.Amount, p.Price.Currency
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO products (`+productColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		p.ID, p.Brand, p.Name, p.Specification, p.Category, aliases, p.Description, priceAmount, priceCurrency, scopes, p.Status, p.CreatedAt, p.UpdatedAt)
	return productDBError(err)
}

func (r *ProductRepository) Save(ctx context.Context, p *domain.Product) error {
	aliases, err := json.Marshal(p.Aliases)
	if err != nil {
		return err
	}
	scopes, err := json.Marshal(p.SalesScopes)
	if err != nil {
		return err
	}
	var priceAmount any
	var priceCurrency any
	if p.Price != nil {
		priceAmount, priceCurrency = p.Price.Amount, p.Price.Currency
	}
	result, err := r.db.ExecContext(ctx, `UPDATE products SET brand=$2,name=$3,specification=$4,category=$5,aliases=$6,description=$7,price_amount=$8,price_currency=$9,sales_scopes=$10,status=$11,updated_at=$12 WHERE id=$1`,
		p.ID, p.Brand, p.Name, p.Specification, p.Category, aliases, p.Description, priceAmount, priceCurrency, scopes, p.Status, p.UpdatedAt)
	if err != nil {
		return productDBError(err)
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return app.ErrNotFound
	}
	return err
}

func (r *ProductRepository) Get(ctx context.Context, id string) (*domain.Product, error) {
	return scanProduct(r.db.QueryRowContext(ctx, `SELECT `+productColumns+` FROM products WHERE id=$1`, id))
}

func (r *ProductRepository) List(ctx context.Context) ([]*domain.Product, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+productColumns+` FROM products`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*domain.Product, 0)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func scanProduct(row interface{ Scan(...any) error }) (*domain.Product, error) {
	var p domain.Product
	var aliases []byte
	var scopes []byte
	var priceAmount sql.NullInt64
	var priceCurrency sql.NullString
	if err := row.Scan(&p.ID, &p.Brand, &p.Name, &p.Specification, &p.Category, &aliases, &p.Description, &priceAmount, &priceCurrency, &scopes, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, app.ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(aliases, &p.Aliases); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(scopes, &p.SalesScopes); err != nil {
		return nil, err
	}
	if priceAmount.Valid && priceCurrency.Valid {
		p.Price = &domain.Money{Amount: priceAmount.Int64, Currency: priceCurrency.String}
	}
	return &p, nil
}

func productDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return app.ErrConflict
	}
	return err
}

var _ app.Repository = (*ProductRepository)(nil)
