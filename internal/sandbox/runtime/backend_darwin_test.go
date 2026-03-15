//go:build darwin

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildExecProfileUsesBroadReadsWhenHomeIsNotVirtualized(t *testing.T) {
	profile := buildExecProfile(ExecLaunchOptions{
		WorkspaceDir: "/workspace",
	})

	if !strings.Contains(profile, "(allow file-read*)") {
		t.Fatalf("expected exec profile to keep broad read baseline, profile: %s", profile)
	}
	if strings.Contains(profile, "(deny file-read*") {
		t.Fatalf("expected exec profile without sandbox home to avoid home deny, profile: %s", profile)
	}
}

func TestBuildExecProfileDeniesRealHomeWhenUsingSandboxHome(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil || realHome == "" {
		t.Fatalf("expected real home for test, err=%v", err)
	}

	workspace := realHome + "/goclaw"
	sandboxHome := realHome + "/.goclaw/sandbox/home"
	profile := buildExecProfile(ExecLaunchOptions{
		WorkspaceDir: workspace,
		HomeDir:      sandboxHome,
	})

	if !strings.Contains(profile, fmt.Sprintf(`(subpath "%s")`, realHome)) {
		t.Fatalf("expected profile to mention denied real home %s, profile: %s", realHome, profile)
	}
	if !strings.Contains(profile, "(deny file-read*") {
		t.Fatalf("expected profile to deny real home reads, profile: %s", profile)
	}
	if !strings.Contains(profile, fmt.Sprintf(`(subpath "%s")`, workspace)) {
		t.Fatalf("expected profile to re-allow workspace %s, profile: %s", workspace, profile)
	}
	if !strings.Contains(profile, fmt.Sprintf(`(subpath "%s")`, sandboxHome)) {
		t.Fatalf("expected profile to re-allow sandbox home %s, profile: %s", sandboxHome, profile)
	}
	if !strings.Contains(profile, `(literal "/dev/null")`) {
		t.Fatalf("expected profile to allow runtime writes to /dev/null, profile: %s", profile)
	}
}

func TestDarwinExecBackendRejectsVolumesMode(t *testing.T) {
	backend := darwinExecBackend{}

	_, err := backend.BuildCommand("true", ExecLaunchOptions{
		SandboxMode: "volumes",
	})
	if err == nil {
		t.Fatal("expected volumes mode to be rejected")
	}
	if !strings.Contains(err.Error(), "autodocs-read") {
		t.Fatalf("expected autodocs guidance, got: %v", err)
	}
}

func TestWriteExecWrapperCleansUpProfileAndSelf(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "exec.sb")

	wrapperPath, err := writeExecWrapper("/usr/bin/true", profilePath, "echo hello")
	if err != nil {
		t.Fatalf("expected wrapper to be created, err=%v", err)
	}

	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("expected wrapper contents, err=%v", err)
	}
	text := string(content)
	if !strings.Contains(text, `rm -f "$PROFILE_PATH" "$0"`) {
		t.Fatalf("expected wrapper to remove profile and self, wrapper: %s", text)
	}
	if !strings.Contains(text, `PROFILE_PATH='`+profilePath+`'`) {
		t.Fatalf("expected wrapper to reference profile path, wrapper: %s", text)
	}
}

func TestBuildWriteRulesExcludesNestedProtectedDirs(t *testing.T) {
	rules := buildWriteRules(
		[]string{"/workspace", "/workspace/skills/scratch"},
		[]string{"/workspace/skills"},
	)

	joined := strings.Join(rules, "\n")
	if strings.Contains(joined, `/workspace/skills/scratch`) {
		t.Fatalf("expected protected child root to be skipped, rules: %s", joined)
	}
	if !strings.Contains(joined, `(require-not (subpath "/workspace/skills"))`) {
		t.Fatalf("expected workspace rule to exclude protected dir, rules: %s", joined)
	}
}

func TestBuildBrowserProfileRespectsProtectedDirs(t *testing.T) {
	profile := buildBrowserProfile("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", BrowserLaunchOptions{
		WorkspaceDir:  "/workspace",
		ProfileDir:    "/tmp/browser-profile",
		ProtectedDirs: []string{"/workspace/skills"},
	})

	if !strings.Contains(profile, `(require-not (subpath "/workspace/skills"))`) {
		t.Fatalf("expected browser profile to exclude protected dir, profile: %s", profile)
	}
}

func TestBuildBrowserProfileIncludesSystemReadRoots(t *testing.T) {
	profile := buildBrowserProfile("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", BrowserLaunchOptions{
		WorkspaceDir: "/workspace",
		ProfileDir:   "/tmp/browser-profile",
	})

	for _, root := range []string{"/bin", "/usr", "/System", "/Library", "/Applications", "/tmp", "/private/tmp", "/var", "/private/var", "/dev"} {
		if !strings.Contains(profile, fmt.Sprintf(`(subpath "%s")`, root)) {
			t.Fatalf("expected browser profile to allow read root %s, profile: %s", root, profile)
		}
	}
}

func TestDarwinBrowserBackendRejectsVolumesMode(t *testing.T) {
	backend := darwinBrowserBackend{}

	_, err := backend.CreateLauncher("/Applications/Test.app/Contents/MacOS/Test", BrowserLaunchOptions{
		SandboxMode: "volumes",
	})
	if err == nil {
		t.Fatal("expected volumes mode to be rejected")
	}
	if !strings.Contains(err.Error(), "autodocs-read") {
		t.Fatalf("expected autodocs guidance, got: %v", err)
	}
}

func TestDarwinBackendRejectsEphemeralMode(t *testing.T) {
	if err := validateDarwinSandboxMode("ephemeral"); err == nil {
		t.Fatal("expected ephemeral mode to be rejected on Darwin")
	}
}

func TestCreateBrowserLauncherWrapsCleanup(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	backend := darwinBrowserBackend{}
	wrapperPath, err := backend.CreateLauncher("/Applications/Test.app/Contents/MacOS/Test", BrowserLaunchOptions{
		SandboxMode:  "home",
		WorkspaceDir: "/workspace",
		ProfileDir:   "/tmp/browser-profile",
		HomeDir:      filepath.Join(tmpHome, ".goclaw", "sandbox", "home"),
	})
	if err != nil {
		t.Fatalf("expected wrapper to be created, err=%v", err)
	}

	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("expected browser wrapper contents, err=%v", err)
	}
	text := string(content)
	if !strings.Contains(text, "rm -f \"$PROFILE_PATH\"") {
		t.Fatalf("expected browser wrapper to remove profile, wrapper: %s", text)
	}
	if !strings.Contains(text, "trap cleanup EXIT INT TERM") {
		t.Fatalf("expected browser wrapper trap, wrapper: %s", text)
	}
}
