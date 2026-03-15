package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyUserSandboxOverride(t *testing.T) {
	tests := []struct {
		name            string
		categoryEnabled bool
		userSandbox     bool
		want            bool
	}{
		{name: "global category off trumps user", categoryEnabled: false, userSandbox: true, want: false},
		{name: "user disable wins when category on", categoryEnabled: true, userSandbox: false, want: false},
		{name: "both enabled", categoryEnabled: true, userSandbox: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplyUserSandboxOverride(tt.categoryEnabled, tt.userSandbox); got != tt.want {
				t.Fatalf("ApplyUserSandboxOverride(%v, %v) = %v, want %v", tt.categoryEnabled, tt.userSandbox, got, tt.want)
			}
		})
	}
}

func TestConfigFormDefUsesGeneralSection(t *testing.T) {
	def := ConfigFormDef()
	if len(def.Sections) == 0 {
		t.Fatal("expected sandbox form sections")
	}
	if def.Sections[0].Title != "General" {
		t.Fatalf("expected first section to be General, got %q", def.Sections[0].Title)
	}
}

func TestSupportedModeOptionsReflectPlatform(t *testing.T) {
	options := SupportedModeOptions()
	if len(options) == 0 {
		t.Fatal("expected mode options")
	}

	switch CurrentSandboxBackend() {
	case BackendSeatbelt:
		if len(options) != 3 || options[0].Value != ModeHome || options[1].Value != ModeAutoDocsRead || options[2].Value != ModeAutoDocsWrite {
			t.Fatalf("expected Darwin seatbelt to expose home and autodocs modes, got %#v", options)
		}
	case BackendBubblewrap:
		if len(options) != 3 {
			t.Fatalf("expected Linux bubblewrap to expose three modes, got %#v", options)
		}
	}
}

func TestGetAutoDocsRootsExcludesHiddenWorkspaceAndSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "goclaw")
	if err := os.MkdirAll(workspace, 0750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	for _, dir := range []string{"Pictures", "Documents", ".ssh"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "Pictures"), filepath.Join(home, "PicturesLink")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	m := &Manager{
		config:        Config{General: GeneralConfig{Mode: ModeAutoDocsRead}},
		workspaceRoot: workspace,
	}

	roots := m.GetAutoDocsRoots()
	got := map[string]bool{}
	for _, root := range roots {
		got[filepath.Base(root)] = true
	}

	if !got["Pictures"] || !got["Documents"] {
		t.Fatalf("expected Pictures and Documents in autodocs roots, got %#v", roots)
	}
	if got["goclaw"] || got["PicturesLink"] || got[".ssh"] {
		t.Fatalf("expected workspace, symlink, and hidden dirs to be excluded, got %#v", roots)
	}
}

func TestValidatePathResolvesAutoDocsTildeToRealHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	sandboxHome := filepath.Join(home, ".goclaw", "sandbox", "home")
	picturesFile := filepath.Join(home, "Pictures", "photo.jpg")
	for _, dir := range []string{workspace, sandboxHome, filepath.Dir(picturesFile), filepath.Join(sandboxHome, ".ssh")} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	m := &Manager{
		config:        Config{General: GeneralConfig{Mode: ModeAutoDocsRead}},
		workspaceRoot: workspace,
		homeDir:       sandboxHome,
	}

	resolved, err := m.ValidatePath("~/Pictures/photo.jpg", workspace)
	if err != nil {
		t.Fatalf("ValidatePath autodocs: %v", err)
	}
	if resolved != picturesFile {
		t.Fatalf("expected autodocs path to resolve to real home, got %q want %q", resolved, picturesFile)
	}

	if _, err = m.ValidatePath("~/.ssh/id_ed25519", workspace); err == nil {
		t.Fatal("expected hidden home key path to remain denied")
	}
}

func TestValidateWritePathBlocksAutoDocsInReadMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	sandboxHome := filepath.Join(home, ".goclaw", "sandbox", "home")
	picturesFile := filepath.Join(home, "Pictures", "photo.jpg")
	for _, dir := range []string{workspace, sandboxHome, filepath.Dir(picturesFile)} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	m := &Manager{
		config:        Config{General: GeneralConfig{Mode: ModeAutoDocsRead}},
		workspaceRoot: workspace,
		homeDir:       sandboxHome,
	}
	if _, err := m.ValidateWritePath("~/Pictures/photo.jpg", workspace); err == nil {
		t.Fatal("expected autodocs read mode to reject writes")
	}

	m.config.General.Mode = ModeAutoDocsWrite
	if _, err := m.ValidateWritePath("~/Pictures/photo.jpg", workspace); err != nil {
		t.Fatalf("expected autodocs write mode to allow writes, err=%v", err)
	}
}
