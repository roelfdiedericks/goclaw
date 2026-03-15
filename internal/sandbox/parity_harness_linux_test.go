//go:build linux

package sandbox

import (
	"testing"
)

func TestLinuxModeParityAcrossFileToolsAndPolicy(t *testing.T) {
	fx := makeParityFixture(t)

	modes := []string{ModeHome, ModeVolumes, ModeEphemeral}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			mgr := makeParityManager(mode, fx)
			policy := mgr.ResolvePolicy()
			if policy.Mode != mode {
				t.Fatalf("expected resolved mode %q, got %q", mode, policy.Mode)
			}
			if policy.VisibleWorkspace != fx.workspace {
				t.Fatalf("expected visible workspace %q, got %q", fx.workspace, policy.VisibleWorkspace)
			}
			if policy.VisibleHomeDir != fx.home {
				t.Fatalf("expected visible home %q, got %q", fx.home, policy.VisibleHomeDir)
			}

			// File tools should always deny protected secret filenames.
			if _, err := mgr.ValidatePath("~/.ssh/id_ed25519", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden secret in mode %s", mode)
			}
			if _, err := mgr.ValidatePath("~/.goclaw/goclaw.json", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden config in mode %s", mode)
			}

			// Workspace writes should stay valid in every mode.
			if err := mgr.WriteFileValidated("linux_ws_probe.txt", fx.workspace, []byte("ws\n"), 0600); err != nil {
				t.Fatalf("expected workspace write in mode %s, err=%v", mode, err)
			}

			// Home path mapping semantics should reflect mode policy.
			switch mode {
			case ModeHome:
				if policy.BackingHomeDir == "" {
					t.Fatalf("expected non-empty backing home in mode %s", mode)
				}
				target, err := mgr.ValidateWritePath("~/Desktop/linux_home_probe.txt", fx.workspace)
				if err != nil {
					t.Fatalf("expected home-mode write path to validate, err=%v", err)
				}
				if target == fx.desktopDoc {
					t.Fatalf("expected home-mode path to map to backing home, got %q", target)
				}
			case ModeVolumes:
				if policy.BackingHomeDir != "" {
					t.Fatalf("expected empty backing home in mode %s, got %q", mode, policy.BackingHomeDir)
				}
				if _, err := mgr.ValidatePath("~/Desktop/doc.txt", fx.workspace); err != nil {
					t.Fatalf("expected desktop read path to validate in mode %s, err=%v", mode, err)
				}
			case ModeEphemeral:
				if policy.BackingHomeDir != "" {
					t.Fatalf("expected empty backing home in mode %s, got %q", mode, policy.BackingHomeDir)
				}
			}
		})
	}
}
