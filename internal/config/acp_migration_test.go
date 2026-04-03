package config

import (
	"encoding/json"
	"testing"

	"github.com/creasty/defaults"
	"github.com/roelfdiedericks/goclaw/internal/acp"
)

func TestMigrateACPConfigJSON(t *testing.T) {
	input := []byte(`{
		"gateway": {
			"acpCursorModel": "claude-4.6-opus-high-thinking"
		}
	}`)

	migrated, err := migrateACPConfigJSON(input)
	if err != nil {
		t.Fatalf("migrate ACP config: %v", err)
	}

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	if err := json.Unmarshal(migrated, cfg); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}

	if cfg.ACP.Drivers.Cursor.Model != "claude-4.6-opus-high-thinking" {
		t.Fatalf("expected ACP cursor model to migrate, got %q", cfg.ACP.Drivers.Cursor.Model)
	}
	if cfg.ACP.DefaultDriver != acp.DriverCursor {
		t.Fatalf("expected default ACP driver %q, got %q", acp.DriverCursor, cfg.ACP.DefaultDriver)
	}
}

func TestMigrateACPConfigJSONPreservesNewModel(t *testing.T) {
	input := []byte(`{
		"gateway": {
			"acpCursorModel": "claude-4.5-opus-high-thinking"
		},
		"acp": {
			"drivers": {
				"cursor": {
					"model": "claude-4.6-opus-high-thinking"
				}
			}
		}
	}`)

	migrated, err := migrateACPConfigJSON(input)
	if err != nil {
		t.Fatalf("migrate ACP config: %v", err)
	}

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	if err := json.Unmarshal(migrated, cfg); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}

	if cfg.ACP.Drivers.Cursor.Model != "claude-4.6-opus-high-thinking" {
		t.Fatalf("expected new ACP model to win, got %q", cfg.ACP.Drivers.Cursor.Model)
	}
}
