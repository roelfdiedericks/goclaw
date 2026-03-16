//go:build linux

package runtime

import (
	"strings"
	"testing"
)

func TestLinuxExecBackendBindsAutodocsRootsByMode(t *testing.T) {
	backend := linuxExecBackend{}
	visibleHome := "/home/tester"
	backingHome := "/sandbox/home"
	autodocsRoot := "/home/tester/Desktop"

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
				WorkspaceDir:   "/workspace",
				WorkDir:        "/workspace",
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
