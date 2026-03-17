//go:build linux

package bwrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/sandbox"
)

func TestExecSandboxKeepsVisibleHomeAndMountsBackingHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	backingHome := filepath.Join(home, ".goclaw", "sandbox", "home")
	if err := os.MkdirAll(workspace, 0750); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(backingHome, 0750); err != nil {
		t.Fatalf("mkdir backing home: %v", err)
	}

	sandbox.InitManager(sandbox.Config{General: sandbox.GeneralConfig{Mode: sandbox.ModeHome}}, workspace)

	b := ExecSandbox(workspace, home, backingHome, sandbox.ModeHome, true, true)
	b.BwrapPath("/bin/true").ShellCommand("pwd")
	_, args, err := b.Build()
	if err != nil {
		t.Fatalf("build sandbox args: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--bind "+backingHome+" "+home) {
		t.Fatalf("expected backing home bind onto visible home, args: %s", joined)
	}
	if !strings.Contains(joined, "--setenv HOME "+home) {
		t.Fatalf("expected visible HOME env, args: %s", joined)
	}
	if !strings.Contains(joined, "--bind "+workspace+" "+workspace) {
		t.Fatalf("expected workspace bind at visible path, args: %s", joined)
	}
}
