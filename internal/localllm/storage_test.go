package localllm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorageLayout(t *testing.T) {
	layout, err := LocalStorageLayout()
	if err != nil {
		t.Fatalf("LocalStorageLayout returned error: %v", err)
	}
	if !strings.HasSuffix(layout.RootDir, filepath.Join(".goclaw", "local")) {
		t.Fatalf("expected local root under ~/.goclaw/local, got %q", layout.RootDir)
	}
	if layout.BinRootDir != filepath.Join(layout.RootDir, "bin") {
		t.Fatalf("unexpected bin root %q", layout.BinRootDir)
	}
	if layout.ModelsRootDir != filepath.Join(layout.RootDir, "models") {
		t.Fatalf("unexpected models root %q", layout.ModelsRootDir)
	}
}

func TestManagedModelPaths(t *testing.T) {
	spec, err := ManagedModelByID("gemma4-e2b")
	if err != nil {
		t.Fatalf("ManagedModelByID returned error: %v", err)
	}

	modelPath, err := ManagedModelPath(spec)
	if err != nil {
		t.Fatalf("ManagedModelPath returned error: %v", err)
	}
	if !strings.HasSuffix(modelPath, filepath.Join(".goclaw", "local", "models", spec.ID, spec.PreferredFilename)) {
		t.Fatalf("unexpected model path %q", modelPath)
	}

	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil {
		t.Fatalf("ManagedModelMMProjPath returned error: %v", err)
	}
	if !strings.HasSuffix(mmprojPath, filepath.Join(".goclaw", "local", "models", spec.ID, spec.MMProjFilename)) {
		t.Fatalf("unexpected mmproj path %q", mmprojPath)
	}
}

func TestManagedModelMMProjPathEmptyWhenOmitted(t *testing.T) {
	spec, err := ManagedModelByID("qwen3-coder-next")
	if err != nil {
		t.Fatalf("ManagedModelByID: %v", err)
	}
	mmprojPath, err := ManagedModelMMProjPath(spec)
	if err != nil {
		t.Fatalf("ManagedModelMMProjPath: %v", err)
	}
	if mmprojPath != "" {
		t.Fatalf("expected empty mmproj path, got %q", mmprojPath)
	}
}

func TestRuntimeBinaryPath(t *testing.T) {
	got, err := RuntimeBinaryPath("b1234", OSLinux, ArchAMD64, BackendCUDA)
	if err != nil {
		t.Fatalf("RuntimeBinaryPath returned error: %v", err)
	}
	wantSuffix := filepath.Join(".goclaw", "local", "bin", "llama.cpp", "b1234-linux-amd64-cuda", "llama-server")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("expected suffix %q, got %q", wantSuffix, got)
	}
}
