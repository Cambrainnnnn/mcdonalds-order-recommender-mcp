package postgres

import (
	"context"
	"errors"
	offers "meal-recommendation/internal/application/offers"
	"testing"
	"time"
)

func TestLegacyDuplicateBackfill(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	seedLegacy(t, db)
	_, err := db.Exec(`INSERT INTO offers SELECT 'duplicate',status,version,jsonb_set(draft,'{id}','"duplicate"'),created_at + interval '1 second',updated_at FROM offers`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM offer_business_keys`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backfill keys=%d %v", count, err)
	}
	repo := NewOfferRepository(db, time.Now, time.Hour, 0, 0)
	all, err := repo.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("legacy rows=%d %v", len(all), err)
	}
	for _, o := range all {
		o.Status = "disabled"
		if err = repo.Save(ctx, o); err != nil {
			t.Fatalf("disable legacy duplicate: %v", err)
		}
	}
	duplicate := *all[0]
	duplicate.ID = "999999"
	if err = repo.Create(ctx, &duplicate); !errors.Is(err, offers.ErrOfferConflict) {
		t.Fatalf("legacy duplicate bypassed dedup: %v", err)
	}
	var snapshots int
	if err = db.QueryRow(`SELECT count(*) FROM offer_versions`).Scan(&snapshots); err != nil || snapshots != 2 {
		t.Fatalf("snapshots changed: %d %v", snapshots, err)
	}
}
