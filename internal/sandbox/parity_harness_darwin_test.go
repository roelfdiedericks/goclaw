//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

func TestDarwinRuntimeParityAcrossModes(t *testing.T) {
	fx := makeParityFixture(t)

	modes := []string{ModeHome, ModeAutoDocsRead, ModeAutoDocsWrite}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			mgr := makeParityManager(mode, fx)
			autoDocsRoots := mgr.GetAutoDocsRoots()

			opts := sbruntime.ExecLaunchOptions{
				SandboxMode:    mode,
				WorkspaceDir:   fx.workspace,
				WorkDir:        fx.workspace,
				VisibleHomeDir: fx.home,
				BackingHomeDir: fx.backingHome,
				ClearEnv:       true,
				PathValue:      os.Getenv("PATH"),
				AllowNetwork:   true,
			}
			switch mode {
			case ModeAutoDocsWrite:
				opts.ExtraBind = append(opts.ExtraBind, autoDocsRoots...)
			case ModeAutoDocsRead:
				opts.ExtraRoBind = append(opts.ExtraRoBind, autoDocsRoots...)
			}

			// File tools must always reject protected secrets.
			if _, err := mgr.ValidatePath("~/.ssh/id_ed25519", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden secret path in mode %s", mode)
			}
			if _, err := mgr.ValidatePath("~/.goclaw/goclaw.json", fx.workspace); err == nil {
				t.Fatalf("expected file tools to block hidden config path in mode %s", mode)
			}

			cmd, err := sbruntime.BuildExecCommand(`echo parity`, opts)
			if err != nil {
				t.Fatalf("build exec command: %v", err)
			}
			if cmd == nil {
				t.Skip("seatbelt unavailable on this host")
			}

			profilePath, err := profilePathFromWrapper(cmd.Path)
			if err != nil {
				t.Fatalf("parse wrapper profile path: %v", err)
			}
			defer os.Remove(cmd.Path)
			defer os.Remove(profilePath)

			profileBytes, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatalf("read generated profile: %v", err)
			}
			profile := string(profileBytes)

			// Generated runtime profile should always include workspace and backing-home write roots.
			if !strings.Contains(profile, `    (subpath "`+filepath.Clean(fx.workspace)+`")`) {
				t.Fatalf("expected profile to include workspace root in mode %s, profile=%s", mode, profile)
			}
			if !strings.Contains(profile, `    (subpath "`+filepath.Clean(fx.backingHome)+`")`) {
				t.Fatalf("expected profile to include backing home root in mode %s, profile=%s", mode, profile)
			}

			if mode == ModeHome {
				// Home mode file tools should resolve to real HOME paths on darwin.
				target, err := mgr.ValidateWritePath("~/home_probe.txt", fx.workspace)
				if err != nil {
					t.Fatalf("expected file tool home write in home mode, err=%v", err)
				}
				if target != filepath.Join(fx.home, "home_probe.txt") {
					t.Fatalf("expected home mode to use real home target, got %q", target)
				}
				// Hidden home paths are blocked by policy in darwin home mode.
				if _, err := mgr.ValidatePath("~/.ssh/rodent.txt", fx.workspace); err == nil {
					t.Fatalf("expected hidden home read deny in mode %s", mode)
				}
				if _, err := mgr.ValidateWritePath("~/.ssh/rodent.txt", fx.workspace); err == nil {
					t.Fatalf("expected hidden home write deny in mode %s", mode)
				}
				return
			}

			// In autodocs modes, desktop reads should resolve to visible-home roots.
			if resolved, err := mgr.ValidatePath("~/Desktop/doc.txt", fx.workspace); err != nil {
				t.Fatalf("expected file tool desktop read in mode %s, err=%v", mode, err)
			} else if resolved != fx.desktopDoc {
				t.Fatalf("expected desktop path to stay on visible home in mode %s, got %q want %q", mode, resolved, fx.desktopDoc)
			}
			if _, err := mgr.ValidatePath("~/.ssh/rodent.txt", fx.workspace); err == nil {
				t.Fatalf("expected hidden home read deny in mode %s", mode)
			}
			if _, err := mgr.ValidateWritePath("~/.ssh/rodent.txt", fx.workspace); err == nil {
				t.Fatalf("expected hidden home write deny in mode %s", mode)
			}

			// Autodocs-read must block writes; autodocs-write must allow writes in file tools.
			writeErr := mgr.WriteFileValidated("~/Desktop/filetool_probe.txt", fx.workspace, []byte("ok\n"), 0600)
			switch mode {
			case ModeAutoDocsRead:
				if writeErr == nil {
					t.Fatalf("expected file tool desktop write to fail in mode %s", mode)
				}
				for _, root := range autoDocsRoots {
					if profileContainsWriteSubpath(profile, root) {
						t.Fatalf("expected profile to keep autodocs root read-only in mode %s, root=%s", mode, root)
					}
				}
			case ModeAutoDocsWrite:
				if writeErr != nil {
					t.Fatalf("expected file tool desktop write to pass in mode %s, err=%v", mode, writeErr)
				}
				for _, root := range autoDocsRoots {
					if !profileContainsWriteSubpath(profile, root) {
						t.Fatalf("expected profile to allow autodocs write root in mode %s, root=%s", mode, root)
					}
				}
			}
		})
	}
}

func profilePathFromWrapper(wrapperPath string) (string, error) {
	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`PROFILE_PATH='([^']+)'`)
	matches := re.FindStringSubmatch(string(content))
	if len(matches) != 2 {
		return "", os.ErrInvalid
	}
	return matches[1], nil
}

func profileContainsWriteSubpath(profile string, path string) bool {
	return strings.Contains(profile, "(allow file-write*\n  (require-all\n    (subpath \""+filepath.Clean(path)+"\")")
}
