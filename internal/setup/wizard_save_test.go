package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWizardConfigToPathWritesOwnerChannelIDs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "goclaw.json")

	data := NewWizardData()
	data.UserName = "owner"
	data.UserDisplayName = "Owner"
	data.UserRole = "owner"
	data.UserTelegramID = "123456789"
	data.UserWhatsAppID = "15551234567"

	if err := SaveWizardConfigToPath(data, configPath); err != nil {
		t.Fatalf("SaveWizardConfigToPath: %v", err)
	}

	usersData, err := os.ReadFile(filepath.Join(tmpDir, "users.json"))
	if err != nil {
		t.Fatalf("read users.json: %v", err)
	}

	var users map[string]map[string]interface{}
	if err := json.Unmarshal(usersData, &users); err != nil {
		t.Fatalf("unmarshal users.json: %v", err)
	}

	owner, ok := users["owner"]
	if !ok {
		t.Fatalf("expected owner user entry")
	}
	if got := owner["telegram_id"]; got != "123456789" {
		t.Fatalf("expected telegram_id to be saved, got %#v", got)
	}
	if got := owner["whatsapp_id"]; got != "15551234567" {
		t.Fatalf("expected whatsapp_id to be saved, got %#v", got)
	}
}
