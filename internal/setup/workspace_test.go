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
