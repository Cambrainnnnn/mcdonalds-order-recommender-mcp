package postgres

import (
	"testing"
	"time"

	appoffers "meal-recommendation/internal/application/offers"
	domain "meal-recommendation/internal/domain/recommendation"
)

func TestOfferMatchesUTCClockAtBeijingLunch(t *testing.T) {
	offer := &appoffers.Offer{
		ValidTimes: []appoffers.ValidTimeRule{{Weekdays: []int{int(time.Tuesday)}, Start: "10:30", End: "00:00"}},
		OrderModes: []domain.OrderMode{domain.OrderModePickup},
	}
	at := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	if !offerMatches(offer, domain.Requirement{}, at) {
		t.Fatal("PostgreSQL's UTC clock incorrectly excluded the Beijing lunch offer")
	}
	if offerMatches(offer, domain.Requirement{OrderMode: domain.OrderModeDelivery}, at) {
		t.Fatal("timezone conversion bypassed the order mode restriction")
	}
	if offerMatches(offer, domain.Requirement{}, at.Add(12*time.Hour)) {
		t.Fatal("offer remained eligible at Beijing midnight")
	}
}
