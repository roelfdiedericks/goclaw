//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

func TestLinuxExecBackendBindsAutodocsRootsByMode(t *testing.T) {
	backend := linuxExecBackend{}
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	visibleHome := filepath.Join(t.TempDir(), "home")
	backingHome := filepath.Join(t.TempDir(), "sandbox-home")
	autodocsRoot := filepath.Join(visibleHome, "Desktop")
	for _, dir := range []string{workspaceDir, visibleHome, backingHome, autodocsRoot} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	sandbox.InitManager(sandbox.Config{
		General: sandbox.GeneralConfig{
			Enabled: true,
			Mode:    sandbox.ModeHome,
		},
	}, workspaceDir)

	tests := []struct {
		name              string
		mode              string
		extraBind         []string
		extraRo           []string
		expectBind        string
		expectRo          string
		expectHomeBind    bool
		expectHomeTmpfs   bool
	}{
		{
			name:            "autodocs-read uses ro bind",
			mode:            "autodocs-read",
			extraRo:         []string{autodocsRoot},
			expectRo:        "--ro-bind " + autodocsRoot + " " + autodocsRoot,
			expectBind:      "",
			expectHomeBind:  false,
			expectHomeTmpfs: true,
		},
		{
			name:            "autodocs-write uses rw bind",
			mode:            "autodocs-write",
			extraBind:       []string{autodocsRoot},
			expectBind:      "--bind " + autodocsRoot + " " + autodocsRoot,
			expectRo:        "",
			expectHomeBind:  false,
			expectHomeTmpfs: true,
		},
		{
			name:            "home mode keeps full home bind",
			mode:            "home",
			expectBind:      "",
			expectRo:        "",
			expectHomeBind:  true,
			expectHomeTmpfs: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := backend.BuildCommand("pwd", ExecLaunchOptions{
				BackendPath:    "/bin/true",
				SandboxMode:    tt.mode,
				WorkspaceDir:   workspaceDir,
				WorkDir:        workspaceDir,
				VisibleHomeDir: visibleHome,
				BackingHomeDir: backingHome,
				ClearEnv:       true,
				PathValue:      "/usr/bin:/bin",
				AllowNetwork:   true,
				ExtraBind:      tt.extraBind,
				ExtraRoBind:    tt.extraRo,
			})
			if err != nil {
				t.Fatalf("build command: %v", err)
			}

			joined := strings.Join(cmd.Args, " ")
			homeBind := "--bind " + backingHome + " " + visibleHome
			if tt.expectHomeBind && !strings.Contains(joined, homeBind) {
				t.Fatalf("expected backing home bind in mode %q, args: %s", tt.mode, joined)
			}
			if !tt.expectHomeBind && strings.Contains(joined, homeBind) {
				t.Fatalf("did not expect full home bind in mode %q, args: %s", tt.mode, joined)
			}
			if tt.expectHomeTmpfs && !strings.Contains(joined, "--tmpfs "+visibleHome) {
				t.Fatalf("expected visible home tmpfs in mode %q, args: %s", tt.mode, joined)
			}
			if !tt.expectHomeTmpfs && strings.Contains(joined, "--tmpfs "+visibleHome) {
				t.Fatalf("did not expect visible home tmpfs in mode %q, args: %s", tt.mode, joined)
			}
			if !strings.Contains(joined, "--setenv HOME "+visibleHome) {
				t.Fatalf("expected visible HOME env, args: %s", joined)
			}

			if tt.expectBind != "" && !strings.Contains(joined, tt.expectBind) {
				t.Fatalf("expected writable autodocs bind %q, args: %s", tt.expectBind, joined)
			}
			if tt.expectRo != "" && !strings.Contains(joined, tt.expectRo) {
				t.Fatalf("expected readonly autodocs bind %q, args: %s", tt.expectRo, joined)
			}
		})
	}
}
