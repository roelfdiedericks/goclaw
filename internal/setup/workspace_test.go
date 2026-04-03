package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateWorkspaceFreshInstallCreatesBootstrapAndTemplates(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "workspace")

	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	for _, dir := range []string{"memory", "skills", "media"} {
		if _, err := os.Stat(filepath.Join(wsPath, dir)); err != nil {
			t.Fatalf("expected subdir %q to exist: %v", dir, err)
		}
	}

	for _, name := range templateFiles {
		if _, err := os.Stat(filepath.Join(wsPath, name)); err != nil {
			t.Fatalf("expected template %q to exist: %v", name, err)
		}
	}
}

func TestCreateWorkspaceRecreatesMissingManagedTemplate(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "workspace")
	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace initial: %v", err)
	}

	target := filepath.Join(wsPath, "USER.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove USER.md: %v", err)
	}

	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace recreate: %v", err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected USER.md to be recreated: %v", err)
	}
}

func TestCreateWorkspaceUpdatesKnownStockCopy(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(wsPath, 0o750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	currentContent, err := LoadTemplateStripped("AGENTS.md")
	if err != nil {
		t.Fatalf("LoadTemplateStripped: %v", err)
	}
	originalManifest := templateManifest
	t.Cleanup(func() {
		templateManifest = originalManifest
	})
	oldContent := "older stock content\n"
	templateManifest = map[string]TemplateManifestEntry{
		"AGENTS.md": {
			Current: checksumString(currentContent),
			Known:   []string{checksumString(oldContent)},
		},
	}

	target := filepath.Join(wsPath, "AGENTS.md")
	if err := os.WriteFile(target, []byte(oldContent), 0o600); err != nil {
		t.Fatalf("write outdated AGENTS.md: %v", err)
	}

	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(got) != currentContent {
		t.Fatalf("expected AGENTS.md to be updated to latest template")
	}
}

func TestCreateWorkspaceSkipsCustomizedTemplate(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(wsPath, 0o750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	currentContent, err := LoadTemplateStripped("TOOLS.md")
	if err != nil {
		t.Fatalf("LoadTemplateStripped: %v", err)
	}
	originalManifest := templateManifest
	t.Cleanup(func() {
		templateManifest = originalManifest
	})
	templateManifest = map[string]TemplateManifestEntry{
		"TOOLS.md": {
			Current: checksumString(currentContent),
			Known:   []string{checksumString("older stock content\n")},
		},
	}

	customContent := "my custom tools file\n"
	target := filepath.Join(wsPath, "TOOLS.md")
	if err := os.WriteFile(target, []byte(customContent), 0o600); err != nil {
		t.Fatalf("write customized TOOLS.md: %v", err)
	}

	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read TOOLS.md: %v", err)
	}
	if string(got) != customContent {
		t.Fatalf("expected customized TOOLS.md to be preserved, got %q", string(got))
	}
}

func TestCreateWorkspaceDoesNotRecreateBootstrapWhenSoulExists(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(wsPath, 0o750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	soulPath := filepath.Join(wsPath, "SOUL.md")
	customSoul := "custom soul"
	if err := os.WriteFile(soulPath, []byte(customSoul), 0o600); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}

	if err := CreateWorkspace(wsPath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wsPath, "BOOTSTRAP.md")); !os.IsNotExist(err) {
		t.Fatalf("expected BOOTSTRAP.md to remain absent when SOUL.md already exists, got err=%v", err)
	}

	gotSoul, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("read SOUL.md: %v", err)
	}
	if string(gotSoul) != customSoul {
		t.Fatalf("expected existing SOUL.md to be preserved, got %q", string(gotSoul))
	}

	for _, name := range []string{"AGENTS.md", "IDENTITY.md", "USER.md", "TOOLS.md", "HEARTBEAT.md"} {
		if _, err := os.Stat(filepath.Join(wsPath, name)); err != nil {
			t.Fatalf("expected template %q to be backfilled: %v", name, err)
		}
	}
}
