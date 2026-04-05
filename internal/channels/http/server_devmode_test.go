package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/runtimeinfo"
)

func TestResolveDevTemplatesDirUsesLaunchCwd(t *testing.T) {
	repoRoot := t.TempDir()
	templatesDir := filepath.Join(repoRoot, filepath.FromSlash(devTemplatesRelativeDir))
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}

	oldLaunch := runtimeinfo.LaunchCwd()
	runtimeinfo.SetLaunchCwd(repoRoot)
	t.Cleanup(func() {
		runtimeinfo.SetLaunchCwd(oldLaunch)
	})

	got, err := resolveDevTemplatesDir()
	if err != nil {
		t.Fatalf("resolveDevTemplatesDir returned error: %v", err)
	}
	if got != templatesDir {
		t.Fatalf("expected %q, got %q", templatesDir, got)
	}
}

func TestResolveDevTemplatesDirUsesLaunchCwdAfterWorkspaceChdir(t *testing.T) {
	repoRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	templatesDir := filepath.Join(repoRoot, filepath.FromSlash(devTemplatesRelativeDir))
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("mkdir templates dir: %v", err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace dir: %v", err)
	}

	oldLaunch := runtimeinfo.LaunchCwd()
	runtimeinfo.SetLaunchCwd(repoRoot)
	t.Cleanup(func() {
		runtimeinfo.SetLaunchCwd(oldLaunch)
	})

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	got, err := resolveDevTemplatesDir()
	if err != nil {
		t.Fatalf("resolveDevTemplatesDir returned error after chdir: %v", err)
	}
	if got != templatesDir {
		t.Fatalf("expected %q, got %q", templatesDir, got)
	}
}

func TestResolveDevTemplatesDirExplainsRepoRootExpectation(t *testing.T) {
	repoRoot := t.TempDir()

	oldLaunch := runtimeinfo.LaunchCwd()
	runtimeinfo.SetLaunchCwd(repoRoot)
	t.Cleanup(func() {
		runtimeinfo.SetLaunchCwd(oldLaunch)
	})

	_, err := resolveDevTemplatesDir()
	if err == nil {
		t.Fatal("expected resolveDevTemplatesDir to fail when html tree is missing")
	}
	if !strings.Contains(err.Error(), "launched from the repo root") {
		t.Fatalf("expected repo-root guidance, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), devTemplatesRelativeDir) {
		t.Fatalf("expected relative dev path in error, got %q", err.Error())
	}
}

func TestHandleStaticJSServesDevAssetFromTemplatesDir(t *testing.T) {
	repoRoot := t.TempDir()
	templatesDir := filepath.Join(repoRoot, filepath.FromSlash(devTemplatesRelativeDir))
	jsDir := filepath.Join(templatesDir, "js")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		t.Fatalf("mkdir js dir: %v", err)
	}

	assetPath := filepath.Join(jsDir, "app.js")
	if err := os.WriteFile(assetPath, []byte("console.log('dev');"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	s := &Server{devMode: true, templatesDir: templatesDir}
	req := httptest.NewRequest(http.MethodGet, "/js/app.js", nil)
	rec := httptest.NewRecorder()

	s.handleStaticJS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "console.log('dev');") {
		t.Fatalf("expected dev asset body, got %q", rec.Body.String())
	}
}
