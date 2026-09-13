package recommendation

import (
	"testing"
	"time"
)

func TestNewSessionAppliesDefaults(t *testing.T) {
	now := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	session, err := NewSession("session-1", "", Requirement{OrderMode: OrderModePickup}, now, 30*time.Minute)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if session.State != SessionCollecting {
		t.Fatalf("State = %q, want %q", session.State, SessionCollecting)
	}
	if session.Requirement.Objectives != DefaultObjectives() {
		t.Fatalf("Objectives = %#v, want defaults", session.Requirement.Objectives)
	}
	if !session.ExpiresAt.Equal(now.UTC().Add(30 * time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", session.ExpiresAt, now.UTC().Add(30*time.Minute))
	}
}

func TestRequirementMissingFields(t *testing.T) {
	requirement := Requirement{OrderMode: OrderModePickup, Location: Location{StoreCode: "store-1"}}
	missing := requirement.MissingFields()
	if len(missing) != 1 || missing[0] != "mustHave" {
		t.Fatalf("MissingFields() = %#v, want [mustHave]", missing)
	}
}

func TestRequirementReadyWithoutLocationAndOrderMode(t *testing.T) {
	r := Requirement{MustHave: []ProductRequest{{ResolvedProductID: "burger", Quantity: 1}}}
	if missing := r.MissingFields(); len(missing) != 0 {
		t.Fatalf("unexpected fields: %v", missing)
	}
	if missing := (Requirement{}).MissingFields(); len(missing) != 1 || missing[0] != "mustHave" {
		t.Fatalf("missing=%v", missing)
	}
}
