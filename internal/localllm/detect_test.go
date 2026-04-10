package localllm

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadLinuxMemTotal(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(tmp, []byte("MemTotal:       12345 kB\n"), 0o644); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	got, err := readLinuxMemTotal(tmp)
	if err != nil {
		t.Fatalf("readLinuxMemTotal returned error: %v", err)
	}
	if want := uint64(12345 * 1024); got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

func TestDetectAvailableBackends(t *testing.T) {
	dir := t.TempDir()
	nvidia := writeExecutable(t, dir, "nvidia-smi", "#!/bin/sh\necho 'CUDA Version: 12.4'\n")
	rocm := writeExecutable(t, dir, "rocminfo", "#!/bin/sh\necho 'Runtime Version: 6.3'\n")
	vulkan := writeExecutable(t, dir, "vulkaninfo", "#!/bin/sh\necho 'Vulkan Instance Version: 1.3.280'\n")

	origExec := execCommand
	origLookPath := lookPath
	execCommand = exec.Command
	lookPath = func(file string) (string, error) {
		switch file {
		case "vulkaninfo":
			return vulkan, nil
		case "nvidia-smi":
			return nvidia, nil
		case "rocminfo":
			return rocm, nil
		default:
			return exec.LookPath(file)
		}
	}
	t.Cleanup(func() {
		execCommand = origExec
		lookPath = origLookPath
	})

	pathEnv := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", pathEnv)

	got := detectAvailableBackends(OSLinux, ArchAMD64)
	want := []Backend{BackendCPU, BackendCUDA, BackendROCm, BackendVulkan}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected backends %v, got %v", want, got)
	}
}

func TestRecommendBackend(t *testing.T) {
	if got := recommendBackend(OSLinux, []Backend{BackendCPU, BackendVulkan}); got != BackendVulkan {
		t.Fatalf("expected vulkan recommendation, got %q", got)
	}
	if got := recommendBackend(OSDarwin, []Backend{BackendCPU, BackendMetal}); got != BackendMetal {
		t.Fatalf("expected metal recommendation, got %q", got)
	}
	if got := recommendBackend(OSLinux, nil); got != BackendCPU {
		t.Fatalf("expected cpu fallback, got %q", got)
	}
}

func writeExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
	return path
}
