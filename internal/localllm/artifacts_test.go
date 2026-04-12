package localllm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLlamaCppArtifact(t *testing.T) {
	tests := []struct {
		name         string
		version      string
		arch         Arch
		osFlavor     OSFlavor
		backend      Backend
		wantBaseURL  string
		wantFilename string
		wantExtra    []string
		wantErr      string
	}{
		{
			name:         "linux amd64 cpu",
			version:      "b1234",
			arch:         ArchAMD64,
			osFlavor:     OSLinux,
			backend:      BackendCPU,
			wantBaseURL:  upstreamReleaseBase + "/b1234",
			wantFilename: "llama-b1234-bin-ubuntu-x64.tar.gz",
		},
		{
			name:         "linux arm64 cuda uses builder",
			version:      "b1234",
			arch:         ArchARM64,
			osFlavor:     OSLinux,
			backend:      BackendCUDA,
			wantBaseURL:  builderReleaseBase + "/b1234",
			wantFilename: "llama-b1234-bin-ubuntu-cuda-arm64.tar.gz",
		},
		{
			name:         "darwin arm64 metal",
			version:      "b1234",
			arch:         ArchARM64,
			osFlavor:     OSDarwin,
			backend:      BackendMetal,
			wantBaseURL:  upstreamReleaseBase + "/b1234",
			wantFilename: "llama-b1234-bin-macos-arm64.tar.gz",
		},
		{
			name:         "windows amd64 cuda with cudart",
			version:      "b1234",
			arch:         ArchAMD64,
			osFlavor:     OSWindows,
			backend:      BackendCUDA,
			wantBaseURL:  upstreamReleaseBase + "/b1234",
			wantFilename: "llama-b1234-bin-win-cuda-13.1-x64.zip",
			wantExtra:    []string{"cudart-llama-bin-win-cuda-13.1-x64.zip"},
		},
		{
			name:     "bookworm amd64 unsupported",
			version:  "b1234",
			arch:     ArchAMD64,
			osFlavor: OSBookworm,
			backend:  BackendCPU,
			wantErr:  "Bookworm AMD64",
		},
		{
			name:         "trixie amd64 rocm same artifact as generic linux",
			version:      "b1234",
			arch:         ArchAMD64,
			osFlavor:     OSTrixie,
			backend:      BackendROCm,
			wantBaseURL:  upstreamReleaseBase + "/b1234",
			wantFilename: "llama-b1234-bin-ubuntu-rocm-7.2-x64.tar.gz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveLlamaCppArtifact(tc.version, tc.arch, tc.osFlavor, tc.backend)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveLlamaCppArtifact returned error: %v", err)
			}
			if got.BaseURL != tc.wantBaseURL {
				t.Fatalf("expected baseURL %q, got %q", tc.wantBaseURL, got.BaseURL)
			}
			if got.Filename != tc.wantFilename {
				t.Fatalf("expected filename %q, got %q", tc.wantFilename, got.Filename)
			}
			if len(got.AdditionalFiles) != len(tc.wantExtra) {
				t.Fatalf("expected extra files %v, got %v", tc.wantExtra, got.AdditionalFiles)
			}
			for i := range tc.wantExtra {
				if got.AdditionalFiles[i] != tc.wantExtra[i] {
					t.Fatalf("expected extra files %v, got %v", tc.wantExtra, got.AdditionalFiles)
				}
			}
		})
	}
}

func TestDetectOSFlavor(t *testing.T) {
	tmp := t.TempDir()
	bookworm := filepath.Join(tmp, "bookworm")
	if err := os.WriteFile(bookworm, []byte("ID=debian\nVERSION_CODENAME=bookworm\n"), 0o644); err != nil {
		t.Fatalf("write os-release: %v", err)
	}
	if got := DetectOSFlavor("linux", bookworm); got != OSBookworm {
		t.Fatalf("expected bookworm, got %q", got)
	}

	trixie := filepath.Join(tmp, "trixie")
	if err := os.WriteFile(trixie, []byte("ID=debian\nVERSION_CODENAME=trixie\n"), 0o644); err != nil {
		t.Fatalf("write os-release: %v", err)
	}
	if got := DetectOSFlavor("linux", trixie); got != OSTrixie {
		t.Fatalf("expected trixie, got %q", got)
	}

	missing := filepath.Join(tmp, "missing")
	if got := DetectOSFlavor("linux", missing); got != OSLinux {
		t.Fatalf("expected linux fallback, got %q", got)
	}
	if got := DetectOSFlavor("darwin", missing); got != OSDarwin {
		t.Fatalf("expected darwin, got %q", got)
	}
}
