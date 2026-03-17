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

func TestConfigFormDefIncludesConditionalSandboxModeSections(t *testing.T) {
	def := ConfigFormDef()

	var foundEnabled bool
	var foundDisabled bool
	for _, section := range def.Sections {
		if section.Title != "Sandbox Mode" {
			continue
		}
		switch section.ShowWhen {
		case "general.enabled=true":
			foundEnabled = true
			if len(section.Fields) != 1 || section.Fields[0].Name != "general.mode" {
				t.Fatalf("expected enabled sandbox mode section to contain general.mode field, got %#v", section.Fields)
			}
		case "general.enabled=false":
			foundDisabled = true
			if section.Desc != "Sandbox mode: not applicable while sandboxing is disabled." {
				t.Fatalf("unexpected disabled sandbox mode message: %q", section.Desc)
			}
		}
	}

	if !foundEnabled {
		t.Fatal("expected sandbox mode section with showWhen general.enabled=true")
	}
	if !foundDisabled {
		t.Fatal("expected sandbox mode section with showWhen general.enabled=false")
	}
}

func TestConfigFormDefIncludesConditionalSandboxCategorySections(t *testing.T) {
	def := ConfigFormDef()

	var foundEnabled bool
	var foundDisabled bool
	for _, section := range def.Sections {
		if section.Title != "Sandbox Categories" {
			continue
		}
		switch section.ShowWhen {
		case "general.enabled=true":
			foundEnabled = true
			if len(section.Fields) != 3 {
				t.Fatalf("expected 3 sandbox category fields, got %d", len(section.Fields))
			}
			if section.Fields[0].Name != "general.execEnabled" ||
				section.Fields[1].Name != "general.browserEnabled" ||
				section.Fields[2].Name != "general.fileToolsEnabled" {
				t.Fatalf("unexpected sandbox category field order/content: %#v", section.Fields)
			}
		case "general.enabled=false":
			foundDisabled = true
			if section.Desc != "Exec, browser, and file tool sandboxing are not applicable while sandboxing is disabled." {
				t.Fatalf("unexpected disabled sandbox categories message: %q", section.Desc)
			}
		}
	}

	if !foundEnabled {
		t.Fatal("expected sandbox categories section with showWhen general.enabled=true")
	}
	if !foundDisabled {
		t.Fatal("expected sandbox categories section with showWhen general.enabled=false")
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
		if len(options) != 5 ||
			options[0].Value != ModeHome ||
			options[1].Value != ModeAutoDocsRead ||
			options[2].Value != ModeAutoDocsWrite ||
			options[3].Value != ModeVolumes ||
			options[4].Value != ModeEphemeral {
			t.Fatalf("expected Linux bubblewrap to expose home/autodocs/volumes/ephemeral modes, got %#v", options)
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

func TestResolvePolicySeparatesVisibleAndBackingHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	backing := filepath.Join(home, ".goclaw", "sandbox", "home")
	m := &Manager{
		config:        Config{General: GeneralConfig{Mode: ModeHome}},
		workspaceRoot: workspace,
		homeDir:       backing,
	}

	policy := m.ResolvePolicy()
	if policy.VisibleHomeDir != filepath.Clean(home) {
		t.Fatalf("expected visible home %q, got %q", filepath.Clean(home), policy.VisibleHomeDir)
	}
	if policy.BackingHomeDir != filepath.Clean(backing) {
		t.Fatalf("expected backing home %q, got %q", filepath.Clean(backing), policy.BackingHomeDir)
	}
	if policy.VisibleWorkspace != filepath.Clean(workspace) {
		t.Fatalf("expected visible workspace %q, got %q", filepath.Clean(workspace), policy.VisibleWorkspace)
	}
}
