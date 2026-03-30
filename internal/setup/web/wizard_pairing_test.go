package web

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/setup"
)

func TestWizardStepsForIncludesPairingOnlyWhenChannelsEnabled(t *testing.T) {
	data := setup.NewWizardData()

	steps := wizardStepsFor(data)
	for _, step := range steps {
		if step.ID == "pairing" {
			t.Fatalf("did not expect pairing step when no pairing channels are enabled")
		}
	}

	data.TelegramEnabled = true
	steps = wizardStepsFor(data)
	found := false
	for _, step := range steps {
		if step.ID == "pairing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pairing step when Telegram is enabled")
	}
}

func TestValidateStepPairingRequiresResolvedOwnerIDs(t *testing.T) {
	data := setup.NewWizardData()
	data.TelegramEnabled = true
	if errs := validateStep("pairing", data); errs["_general"] == "" {
		t.Fatalf("expected pairing validation error for Telegram")
	}

	data.UserTelegramID = "123"
	data.TelegramEnabled = false
	data.WhatsAppEnabled = true
	data.UserWhatsAppID = ""
	if errs := validateStep("pairing", data); errs["_general"] == "" {
		t.Fatalf("expected pairing validation error for WhatsApp")
	}

	data.UserWhatsAppID = "15551234567"
	if errs := validateStep("pairing", data); len(errs) != 0 {
		t.Fatalf("expected pairing validation to pass, got %#v", errs)
	}
}
