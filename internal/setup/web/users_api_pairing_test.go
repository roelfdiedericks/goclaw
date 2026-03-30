package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestHandleUpdateOwnerPairingUpdatesOwnerIDs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "goclaw.json")
	usersPath := filepath.Join(tmpDir, "users.json")
	users := user.UsersConfig{
		"owner": {
			Name: "Owner",
			Role: "owner",
		},
	}
	if err := user.SaveUsers(users, usersPath); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	api := NewUsersAPI(configPath)
	body, err := json.Marshal(UpdateOwnerPairingRequest{
		TelegramID: "123456789",
		WhatsAppID: "15551234567",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/setup/api/users/owner-pairing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.HandleUpdateOwnerPairing(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	updated, err := user.LoadUsersFromPath(usersPath)
	if err != nil {
		t.Fatalf("LoadUsersFromPath: %v", err)
	}
	if got := updated["owner"].TelegramID; got != "123456789" {
		t.Fatalf("expected telegram id to be updated, got %q", got)
	}
	if got := updated["owner"].WhatsAppID; got != "15551234567" {
		t.Fatalf("expected whatsapp id to be updated, got %q", got)
	}
}
