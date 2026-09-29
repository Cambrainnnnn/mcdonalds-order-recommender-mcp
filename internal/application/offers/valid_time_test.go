package offers_test

import (
	"testing"
	"time"

	appoffers "meal-recommendation/internal/application/offers"
)

func TestValidTimesUseBeijingTime(t *testing.T) {
	lunch := []appoffers.ValidTimeRule{{Weekdays: []int{int(time.Tuesday)}, Start: "10:30", End: "00:00"}}
	earlyMonday := []appoffers.ValidTimeRule{{Weekdays: []int{int(time.Monday)}, Start: "00:00", End: "02:00"}}
	overnight := []appoffers.ValidTimeRule{{Start: "22:00", End: "02:00"}}
	for _, tc := range []struct {
		name  string
		rules []appoffers.ValidTimeRule
		at    string
		want  bool
	}{
		{"UTC noon in China", lunch, "2026-09-29T04:00:00Z", true},
		{"explicit Beijing offset", lunch, "2026-09-29T12:00:00+08:00", true},
		{"same instant in negative offset", lunch, "2026-09-29T00:00:00-04:00", true},
		{"before opening", lunch, "2026-09-29T02:29:59Z", false},
		{"inclusive opening", lunch, "2026-09-29T02:30:00Z", true},
		{"before midnight", lunch, "2026-09-29T15:59:59Z", true},
		{"exclusive midnight", lunch, "2026-09-29T16:00:00Z", false},
		{"local Monday despite UTC Sunday", earlyMonday, "2026-09-27T16:30:00Z", true},
		{"local Tuesday despite UTC Monday", earlyMonday, "2026-09-28T16:30:00Z", false},
		{"overnight evening", overnight, "2026-09-29T14:00:00Z", true},
		{"overnight morning", overnight, "2026-09-29T17:00:00Z", true},
		{"overnight exclusive end", overnight, "2026-09-29T18:00:00Z", false},
		{"unrestricted", nil, "2026-09-29T04:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := appoffers.MatchesValidTime(tc.rules, at); got != tc.want {
				t.Fatalf("MatchesValidTime(%s) = %v, want %v", tc.at, got, tc.want)
			}
		})
	}
}
