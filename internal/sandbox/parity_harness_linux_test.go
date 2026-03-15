//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

func TestLinuxRuntimeParityAcrossModes(t *testing.T) {
	fx := makeParityFixture(t)

	modes := []string{ModeHome, ModeVolumes, ModeEphemeral}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			resetSandboxManagerForTest()
			cfg := Config{
				General: GeneralConfig{
					Enabled:          true,
					Mode:             mode,
					DataDir:          fx.volumesData,
					ExecEnabled:      true,
					BrowserEnabled:   true,
					FileToolsEnabled: true,
				},
				Bubblewrap: BubblewrapConfig{
					Volumes: []string{"~/Desktop"},
				},
			}
			mgr := InitManager(cfg, fx.workspace)

			if !sbruntime.ExecSandboxAvailable(mgr.GetBackendPath()) {
				t.Skip("bubblewrap unavailable on this host")
			}

			policy := mgr.ResolvePolicy()
			opts := sbruntime.ExecLaunchOptions{
				BackendPath:    mgr.GetBackendPath(),
				SandboxMode:    mode,
				WorkspaceDir:   fx.workspace,
				WorkDir:        fx.workspace,
				VisibleHomeDir: policy.VisibleHomeDir,
				BackingHomeDir: policy.BackingHomeDir,
				ClearEnv:       true,
				PathValue:      mgr.BuildSandboxPATH(policy.VisibleHomeDir),
				AllowNetwork:   true,
			}

			homeExit, homeOut := runSandboxedCommand(t, `printf "%s" "$HOME"`, opts)
			if homeExit != 0 {
				t.Fatalf("expected HOME probe to run in mode %s", mode)
			}
			if homeOut != fx.home {
				t.Fatalf("expected visible HOME %q in mode %s, got %q", fx.home, mode, homeOut)
			}

			// File tools should always deny protected secret filenames.
			if _, err := mgr.ValidatePath("~/.ssh/id_ed25519", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden secret in mode %s", mode)
			}
			if _, err := mgr.ValidatePath("~/.goclaw/goclaw.json", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden config in mode %s", mode)
			}

			secretExit, _ := runSandboxedCommand(t, `cat "$HOME/.ssh/id_ed25519"`, opts)
			if secretExit == 0 {
				t.Fatalf("expected exec to block hidden secret read in mode %s", mode)
			}

			desktopReadExit, _ := runSandboxedCommand(t, `cat "$HOME/Desktop/doc.txt"`, opts)
			desktopWriteErr := mgr.WriteFileValidated("~/Desktop/linux_filetool_probe.txt", fx.workspace, []byte("ok\n"), 0600)
			documentsWriteErr := mgr.WriteFileValidated("~/Documents/blocked_probe.txt", fx.workspace, []byte("no\n"), 0600)
			documentsExecExit, _ := runSandboxedCommand(t, `echo nope > "$HOME/Documents/blocked_exec_probe.txt"`, opts)

			switch mode {
			case ModeHome:
				if desktopReadExit != 0 {
					t.Fatalf("expected desktop read in mode %s", mode)
				}
				if desktopWriteErr != nil {
					t.Fatalf("expected desktop file-tool write in mode %s, err=%v", mode, desktopWriteErr)
				}
				// Home mode allows any HOME subpath in both file tools and exec.
				if documentsWriteErr != nil {
					t.Fatalf("expected documents file-tool write in mode %s, err=%v", mode, documentsWriteErr)
				}
				if documentsExecExit != 0 {
					t.Fatalf("expected documents exec write in mode %s", mode)
				}
			case ModeVolumes:
				if desktopReadExit != 0 {
					t.Fatalf("expected desktop read in mode %s", mode)
				}
				if desktopWriteErr != nil {
					t.Fatalf("expected desktop file-tool write in mode %s, err=%v", mode, desktopWriteErr)
				}
				if documentsWriteErr == nil {
					t.Fatalf("expected documents file-tool write denial in mode %s", mode)
				}
				if documentsExecExit == 0 {
					t.Fatalf("expected documents exec write denial in mode %s", mode)
				}
			case ModeEphemeral:
				if desktopReadExit == 0 {
					t.Fatalf("expected desktop read denial in mode %s", mode)
				}
				if desktopWriteErr == nil {
					t.Fatalf("expected desktop file-tool write denial in mode %s", mode)
				}
				if documentsWriteErr == nil {
					t.Fatalf("expected documents file-tool write denial in mode %s", mode)
				}
				if documentsExecExit == 0 {
					t.Fatalf("expected documents exec write denial in mode %s", mode)
				}
			}

			_ = os.Remove(filepath.Join(fx.desktopDir, "linux_filetool_probe.txt"))
			_ = os.Remove(filepath.Join(fx.documentsDir, "blocked_probe.txt"))
			_ = os.Remove(filepath.Join(fx.documentsDir, "blocked_exec_probe.txt"))
		})
	}
}
