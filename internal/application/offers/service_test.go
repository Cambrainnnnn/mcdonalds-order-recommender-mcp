package offers_test

import (
	"context"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	appoffers "meal-recommendation/internal/application/offers"
	domain "meal-recommendation/internal/domain/recommendation"
)

type fixedID string

func (id fixedID) NewID() (string, error) { return string(id), nil }

func TestOfferLifecyclePublishesImmutableVersions(t *testing.T) {
	now := time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)
	repository := memory.NewOfferRepository(func() time.Time { return now }, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-1"), func() time.Time { return now })
	offer, err := service.Create(context.Background(), appoffers.CreateCommand{Source: "douyin", Title: "巨无霸套餐", Price: domain.Money{Amount: 2990, Currency: "CNY"}, OrderModes: []domain.OrderMode{domain.OrderModePickup}, Components: []appoffers.Component{{CanonicalProductID: "BIG_MAC", Quantity: 1}}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if offer.Status != appoffers.StatusDraft {
		t.Fatalf("status=%q", offer.Status)
	}
	if _, err = service.Review(context.Background(), offer.ID); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	published, err := service.Publish(context.Background(), offer.ID)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if published.Version != 1 || published.Status != appoffers.StatusPublished {
		t.Fatalf("published=%#v", published)
	}
	eligible, err := repository.ListEligible(context.Background(), domain.Requirement{OrderMode: domain.OrderModePickup})
	if err != nil || len(eligible) != 1 {
		t.Fatalf("eligible=%#v err=%v", eligible, err)
	}
	now = now.Add(time.Hour)
	verified, err := service.Verify(context.Background(), offer.ID)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.Version != 2 {
		t.Fatalf("version=%d want 2", verified.Version)
	}
	if _, err = service.Disable(context.Background(), offer.ID); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	eligible, err = repository.ListEligible(context.Background(), domain.Requirement{OrderMode: domain.OrderModePickup})
	if err != nil || len(eligible) != 0 {
		t.Fatalf("eligible after disable=%#v err=%v", eligible, err)
	}
}

func TestOfferRejectsInvalidTransition(t *testing.T) {
	repository := memory.NewOfferRepository(time.Now, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-1"), time.Now)
	offer, err := service.Create(context.Background(), appoffers.CreateCommand{Source: "meituan", Title: "套餐", Price: domain.Money{Amount: 1000, Currency: "CNY"}, Components: []appoffers.Component{{CanonicalProductID: "BURGER", Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(context.Background(), offer.ID); err == nil {
		t.Fatal("Publish() should reject a draft")
	}
}

func TestOfferCatalogUsesReservationTimeAndDailyWindow(t *testing.T) {
	now := time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)
	validFrom := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)
	repository := memory.NewOfferRepository(func() time.Time { return now }, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-window"), func() time.Time { return now })
	offer, err := service.Create(context.Background(), appoffers.CreateCommand{
		Source: "douyin", Title: "午餐套餐", Price: domain.Money{Amount: 1990, Currency: "CNY"},
		ValidFrom: &validFrom, ValidUntil: &validUntil,
		ValidTimes: []appoffers.ValidTimeRule{{Weekdays: []int{int(time.Monday)}, Start: "10:30", End: "14:00"}},
		Components: []appoffers.Component{{CanonicalProductID: "BURGER", Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(context.Background(), offer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(context.Background(), offer.ID); err != nil {
		t.Fatal(err)
	}

	inside := time.Date(2026, time.September, 7, 11, 0, 0, 0, time.UTC)
	eligible, err := repository.ListEligible(context.Background(), domain.Requirement{TargetTime: &inside})
	if err != nil || len(eligible) != 1 {
		t.Fatalf("inside window: eligible=%#v err=%v", eligible, err)
	}
	outside := time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC)
	eligible, err = repository.ListEligible(context.Background(), domain.Requirement{TargetTime: &outside})
	if err != nil || len(eligible) != 0 {
		t.Fatalf("outside window: eligible=%#v err=%v", eligible, err)
	}
}

func TestOfferRejectsInvalidDailyWindow(t *testing.T) {
	repository := memory.NewOfferRepository(time.Now, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-1"), time.Now)
	_, err := service.Create(context.Background(), appoffers.CreateCommand{
		Source: "meituan", Title: "套餐", Price: domain.Money{Amount: 1000, Currency: "CNY"},
		ValidTimes: []appoffers.ValidTimeRule{{Start: "10:00", End: "10:00"}},
		Components: []appoffers.Component{{CanonicalProductID: "BURGER", Quantity: 1}},
	})
	if err == nil {
		t.Fatal("Create() should reject an empty daily window")
	}
}
