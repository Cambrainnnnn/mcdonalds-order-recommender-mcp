package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	app "meal-recommendation/internal/application/offers"
)

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *OfferRepository) query() executor {
	if r.tx != nil {
		return r.tx
	}
	return r.db
}

func (r *OfferRepository) write(ctx context.Context, o *app.Offer, create bool) error {
	if r.tx == nil {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `LOCK TABLE offers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		scoped := OfferRepository{db: r.db, tx: tx}
		if err = scoped.write(ctx, o, create); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		r.invalidate()
		return nil
	}
	existing, err := r.List(ctx)
	if err != nil {
		return err
	}
	key := app.BusinessKey(o)
	identityChanged := true
	for _, v := range existing {
		if v.ID == o.ID && app.BusinessKey(v) == key {
			identityChanged = false
		}
	}
	if create || identityChanged {
		for _, v := range existing {
			if v.ID != o.ID && app.BusinessKey(v) == key {
				return app.ErrOfferConflict
			}
		}
	}
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if create {
		_, err = r.tx.ExecContext(ctx, `INSERT INTO offers (id,status,version,draft,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6)`, o.ID, o.Status, o.Version, payload, o.CreatedAt, o.UpdatedAt)
	} else {
		var result sql.Result
		result, err = r.tx.ExecContext(ctx, `UPDATE offers SET status=$2,version=$3,draft=$4,updated_at=$5 WHERE id=$1`, o.ID, o.Status, o.Version, payload, o.UpdatedAt)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n == 0 {
				return app.ErrOfferNotFound
			}
		}
	}
	if err != nil {
		return offerDBError(err)
	}
	if !create && !identityChanged {
		return nil
	}
	_, err = r.tx.ExecContext(ctx, `INSERT INTO offer_business_keys (business_key,offer_id) VALUES ($1,$2) ON CONFLICT (offer_id) DO UPDATE SET business_key=EXCLUDED.business_key`, key, o.ID)
	return offerDBError(err)
}

func offerDBError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return app.ErrOfferConflict
	}
	return err
}

// Historical duplicates remain readable. The oldest row owns the key; future
// creates compare every legacy row and cannot introduce another duplicate.
func backfillOfferKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT draft FROM offers ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	values := []app.Offer{}
	for rows.Next() {
		var b []byte
		var o app.Offer
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &o); err != nil {
			rows.Close()
			return err
		}
		values = append(values, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range values {
		if _, err = tx.ExecContext(ctx, `INSERT INTO offer_business_keys (business_key,offer_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, app.BusinessKey(&o), o.ID); err != nil {
			return err
		}
	}
	return nil
}
