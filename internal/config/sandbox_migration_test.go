package config

import (
	"encoding/json"
	"testing"

	"github.com/creasty/defaults"
)

func TestMigrateSandboxConfigJSON(t *testing.T) {
	input := []byte(`{
		"sandbox": {
			"bubblewrap": {
				"path": "/custom/backend",
				"mode": "volumes",
				"dataDir": "~/.goclaw/sandbox",
				"extraPaths": ["~/.local/bin"],
				"volumes": ["~/.config"]
			}
		},
		"tools": {
			"exec": { "bubblewrap": { "enabled": false } },
			"browser": { "bubblewrap": { "enabled": true } }
		}
	}`)

	migrated, err := migrateSandboxConfigJSON(input)
	if err != nil {
		t.Fatalf("migrate sandbox config: %v", err)
	}

	cfg := &Config{}
	if err := defaults.Set(cfg); err != nil {
		t.Fatalf("defaults.Set: %v", err)
	}
	if err := json.Unmarshal(migrated, cfg); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}

	if cfg.Sandbox.General.Mode != "volumes" {
		t.Fatalf("expected general.mode to migrate, got %q", cfg.Sandbox.General.Mode)
	}
	if cfg.Sandbox.General.DataDir != "~/.goclaw/sandbox" {
		t.Fatalf("expected general.dataDir to migrate, got %q", cfg.Sandbox.General.DataDir)
	}
	if len(cfg.Sandbox.General.ExtraPaths) != 1 || cfg.Sandbox.General.ExtraPaths[0] != "~/.local/bin" {
		t.Fatalf("expected general.extraPaths to migrate, got %#v", cfg.Sandbox.General.ExtraPaths)
	}
	if cfg.Sandbox.General.ExecEnabled {
		t.Fatalf("expected execEnabled to migrate from old false toggle")
	}
	if !cfg.Sandbox.General.BrowserEnabled {
		t.Fatalf("expected browserEnabled to migrate from old true toggle")
	}
	if cfg.Sandbox.Bubblewrap.Path != "/custom/backend" {
		t.Fatalf("expected bubblewrap.path to be preserved, got %q", cfg.Sandbox.Bubblewrap.Path)
	}
	if cfg.Sandbox.Seatbelt.Path != "/custom/backend" {
		t.Fatalf("expected seatbelt.path fallback to be populated, got %q", cfg.Sandbox.Seatbelt.Path)
	}
	if len(cfg.Sandbox.Bubblewrap.Volumes) != 1 || cfg.Sandbox.Bubblewrap.Volumes[0] != "~/.config" {
		t.Fatalf("expected bubblewrap.volumes to be preserved, got %#v", cfg.Sandbox.Bubblewrap.Volumes)
	}
}
