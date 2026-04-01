package user

import "testing"

func TestUserEntryApplyDefaultsSetsACPAllowedByRole(t *testing.T) {
	owner := &UserEntry{Role: "owner"}
	owner.applyDefaults()
	if owner.ACPAllowed == nil || !*owner.ACPAllowed {
		t.Fatalf("expected owner ACP allowed default to be true")
	}

	guest := &UserEntry{Role: "guest"}
	guest.applyDefaults()
	if guest.ACPAllowed == nil || *guest.ACPAllowed {
		t.Fatalf("expected non-owner ACP allowed default to be false")
	}
}
