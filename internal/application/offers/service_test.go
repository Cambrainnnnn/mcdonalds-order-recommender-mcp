package offers_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	appoffers "meal-recommendation/internal/application/offers"
	domain "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/ids"
)

type fixedID string

func (id fixedID) NewID() (string, error) { return string(id), nil }

func TestOfferLifecyclePublishesImmutableVersions(t *testing.T) {
	now := time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)
	repository := memory.NewOfferRepository(func() time.Time { return now }, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-1"), func() time.Time { return now })
	offer, err := service.Create(context.Background(), appoffers.CreateCommand{Source: "douyin", Title: "巨无霸套餐", Price: domain.Money{Amount: 2990, Currency: "CNY"}, OrderModes: []domain.OrderMode{domain.OrderModePickup}, Components: []appoffers.Component{{CanonicalProductID: "221458923456789012", Quantity: 1}}})
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
	if _, err := service.Publish(context.Background(), offer.ID); !errors.Is(err, appoffers.ErrInvalidTransition) {
		t.Fatalf("disabled offer bypassed review: %v", err)
	}
	if _, err := service.Review(context.Background(), offer.ID); err != nil {
		t.Fatalf("review disabled offer: %v", err)
	}
	republished, err := service.Publish(context.Background(), offer.ID)
	if err != nil || republished.Version != 3 || republished.Status != appoffers.StatusPublished {
		t.Fatalf("republish=%+v err=%v", republished, err)
	}
	eligible, err = repository.ListEligible(context.Background(), domain.Requirement{OrderMode: domain.OrderModePickup})
	if err != nil || len(eligible) != 1 {
		t.Fatalf("eligible after republish=%#v err=%v", eligible, err)
	}
}

func TestListOffersOrdersByMostRecentlyUpdated(t *testing.T) {
	repository := memory.NewOfferRepository(time.Now, 24*time.Hour)
	older := &appoffers.Offer{ID: "offer-b", Title: "older", UpdatedAt: time.Date(2026, time.September, 5, 8, 0, 0, 0, time.UTC)}
	newer := &appoffers.Offer{ID: "offer-a", Title: "newer", UpdatedAt: older.UpdatedAt.Add(time.Hour)}
	if err := repository.Create(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	service := appoffers.NewService(repository, fixedID("unused"), time.Now)
	offers, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(offers) != 2 || offers[0].ID != "offer-a" || offers[1].ID != "offer-b" {
		t.Fatalf("offers=%#v", offers)
	}
}

func TestOfferRejectsInvalidTransition(t *testing.T) {
	repository := memory.NewOfferRepository(time.Now, 24*time.Hour)
	service := appoffers.NewService(repository, fixedID("offer-1"), time.Now)
	offer, err := service.Create(context.Background(), appoffers.CreateCommand{Source: "meituan", Title: "套餐", Price: domain.Money{Amount: 1000, Currency: "CNY"}, Components: []appoffers.Component{{CanonicalProductID: "221458923456789012", Quantity: 1}}})
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
		Components: []appoffers.Component{{CanonicalProductID: "221458923456789012", Quantity: 1}},
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

	inside := time.Date(2026, time.September, 7, 3, 0, 0, 0, time.UTC) // 11:00 Beijing
	eligible, err := repository.ListEligible(context.Background(), domain.Requirement{TargetTime: &inside})
	if err != nil || len(eligible) != 1 {
		t.Fatalf("inside window: eligible=%#v err=%v", eligible, err)
	}
	outside := time.Date(2026, time.September, 7, 7, 0, 0, 0, time.UTC) // 15:00 Beijing
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
		Components: []appoffers.Component{{CanonicalProductID: "221458923456789012", Quantity: 1}},
	})
	if err == nil {
		t.Fatal("Create() should reject an empty daily window")
	}
}

func TestOfferValidatesExactChoiceGroupsAndSurcharges(t *testing.T) {
	repository := memory.NewOfferRepository(time.Now, 24*time.Hour)
	service := appoffers.NewService(repository, ids.Default, time.Now)
	command := appoffers.CreateCommand{
		Source: "meituan", Title: "小食三选二", Price: domain.Money{Amount: 1000, Currency: "CNY"},
		ChoiceGroups: []appoffers.ChoiceGroup{{ID: "221458923456789010", Name: "小食三选二", MinSelect: 2, MaxSelect: 2}},
		Components: []appoffers.Component{
			{CanonicalProductID: "221458923456789011", Quantity: 1, ChoiceGroupID: "221458923456789010"},
			{CanonicalProductID: "221458923456789012", Quantity: 1, ChoiceGroupID: "221458923456789010", ExtraPrice: 100},
			{CanonicalProductID: "221458923456789013", Quantity: 1, ChoiceGroupID: "221458923456789010", ExtraPrice: 200},
		},
	}
	offer, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if offer.ChoiceGroups[0].Name != "小食三选二" || offer.Components[2].ExtraPrice != 200 {
		t.Fatalf("offer=%#v", offer)
	}

	invalid := command
	invalid.Components = slices.Clone(command.Components)
	invalid.Components[0].ChoiceGroupID = "221458923456789099"
	if _, err := service.Create(context.Background(), invalid); !errors.Is(err, appoffers.ErrInvalidOffer) {
		t.Fatalf("unknown choice group accepted: %v", err)
	}

	invalid = command
	invalid.Components = slices.Clone(command.Components)
	invalid.Components[0].ExtraPrice = -1
	if _, err := service.Create(context.Background(), invalid); !errors.Is(err, appoffers.ErrInvalidOffer) {
		t.Fatalf("negative surcharge accepted: %v", err)
	}

	invalid = command
	invalid.ChoiceGroups = []appoffers.ChoiceGroup{{ID: "221458923456789010", MinSelect: 4, MaxSelect: 4}}
	if _, err := service.Create(context.Background(), invalid); !errors.Is(err, appoffers.ErrInvalidOffer) {
		t.Fatalf("oversized selection accepted: %v", err)
	}

	legacy := command
	legacy.ChoiceGroups = nil
	if _, err := service.Create(context.Background(), legacy); err != nil {
		t.Fatalf("legacy grouped components should remain valid: %v", err)
	}
}

func TestUpdateDisabledOfferPreservesVersion(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewOfferRepository(time.Now, time.Hour)
	original := &appoffers.Offer{ID: "offer-1", Status: appoffers.StatusDisabled, Version: 4, CreatedAt: time.Now().Add(-time.Hour)}
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	service := appoffers.NewService(repo, fixedID("version-5"), time.Now)
	updated, err := service.UpdateDraft(ctx, original.ID, appoffers.UpdateCommand{Source: "manual", Title: "修复套餐", Price: domain.Money{Amount: 1990, Currency: "CNY"}, Components: []appoffers.Component{{CanonicalProductID: "221458923456789012", Quantity: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != appoffers.StatusDraft || updated.Version != 4 || !updated.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := service.Review(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(ctx, original.ID)
	if err != nil || published.Version != 5 {
		t.Fatalf("published=%+v err=%v", published, err)
	}
}
