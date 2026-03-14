package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/auth"
	"github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestNormalizeTildePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("get home dir: %v", err)
	}

	cfg := &Config{
		Gateway: types.GatewayConfig{
			LogFile:    "~/.goclaw/goclaw.log",
			PIDFile:    "~/.goclaw/goclaw.pid",
			WorkingDir: "~/.goclaw/workspace",
		},
		Session: session.SessionConfig{
			StorePath:   "~/.goclaw/sessions.db",
			InheritPath: "~/.openclaw/agents/main/sessions",
		},
		Auth: auth.AuthConfig{
			Script: "~/scripts/validate.sh",
		},
		Sandbox: sandbox.Config{
			Bubblewrap: sandbox.BubblewrapConfig{
				DataDir:    "~/.goclaw/sandbox",
				ExtraPaths: []string{"~/.local/bin", "/usr/local/bin"},
				Volumes:    []string{"~/.config", "~/.cache"},
			},
		},
		Roles: user.RolesConfig{
			"guest": {
				SystemPromptFile: "~/prompts/guest.md",
			},
		},
	}

	if err := normalizeTildePaths(cfg); err != nil {
		t.Fatalf("normalize tilde paths: %v", err)
	}

	tests := map[string]string{
		"gateway.logFile":             cfg.Gateway.LogFile,
		"gateway.pidFile":             cfg.Gateway.PIDFile,
		"gateway.workingDir":          cfg.Gateway.WorkingDir,
		"session.storePath":           cfg.Session.StorePath,
		"session.inheritPath":         cfg.Session.InheritPath,
		"auth.script":                 cfg.Auth.Script,
		"sandbox.bubblewrap.dataDir":  cfg.Sandbox.Bubblewrap.DataDir,
		"roles.guest.systemPromptFile": cfg.Roles["guest"].SystemPromptFile,
	}

	for name, got := range tests {
		if filepath.IsAbs(got) == false {
			t.Fatalf("%s not absolute after normalization: %q", name, got)
		}
		if len(got) < len(home) || got[:len(home)] != home {
			t.Fatalf("%s does not start with home dir: got %q home %q", name, got, home)
		}
	}

	for i, got := range cfg.Sandbox.Bubblewrap.ExtraPaths {
		if i == 0 {
			if filepath.IsAbs(got) == false || got[:len(home)] != home {
				t.Fatalf("extraPaths[%d] not expanded: %q", i, got)
			}
			continue
		}
		if got != "/usr/local/bin" {
			t.Fatalf("extraPaths[%d] changed unexpectedly: %q", i, got)
		}
	}

	for i, got := range cfg.Sandbox.Bubblewrap.Volumes {
		if filepath.IsAbs(got) == false || got[:len(home)] != home {
			t.Fatalf("volumes[%d] not expanded: %q", i, got)
		}
	}
}
