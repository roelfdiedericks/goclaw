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
		name       string
		extraBind  []string
		extraRo    []string
		expectBind string
		expectRo   string
	}{
		{
			name:       "autodocs-read uses ro bind",
			extraRo:    []string{autodocsRoot},
			expectRo:   "--ro-bind " + autodocsRoot + " " + autodocsRoot,
			expectBind: "",
		},
		{
			name:       "autodocs-write uses rw bind",
			extraBind:  []string{autodocsRoot},
			expectBind: "--bind " + autodocsRoot + " " + autodocsRoot,
			expectRo:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := backend.BuildCommand("pwd", ExecLaunchOptions{
				BackendPath:    "/bin/true",
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
			if !strings.Contains(joined, "--bind "+backingHome+" "+visibleHome) {
				t.Fatalf("expected backing home bind, args: %s", joined)
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
