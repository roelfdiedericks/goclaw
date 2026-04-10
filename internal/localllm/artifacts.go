package localllm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	upstreamReleaseBase = "https://github.com/ggml-org/llama.cpp/releases/download"
	builderReleaseBase  = "https://github.com/hybridgroup/llama-cpp-builder/releases/download"
)

type Arch string

const (
	ArchAMD64 Arch = "amd64"
	ArchARM64 Arch = "arm64"
)

type Backend string

const (
	BackendCPU    Backend = "cpu"
	BackendCUDA   Backend = "cuda"
	BackendMetal  Backend = "metal"
	BackendROCm   Backend = "rocm"
	BackendVulkan Backend = "vulkan"
)

type OSFlavor string

const (
	OSLinux    OSFlavor = "linux"
	OSBookworm OSFlavor = "bookworm"
	OSTrixie   OSFlavor = "trixie"
	OSDarwin   OSFlavor = "darwin"
	OSWindows  OSFlavor = "windows"
)

type ArtifactSpec struct {
	BaseURL         string
	Filename        string
	AdditionalFiles []string
}

func CurrentArch() Arch {
	switch runtime.GOARCH {
	case string(ArchARM64):
		return ArchARM64
	default:
		return ArchAMD64
	}
}

func CurrentOSFlavor() OSFlavor {
	return DetectOSFlavor(runtime.GOOS, defaultOSReleasePath)
}

func DetectOSFlavor(goos, osReleasePath string) OSFlavor {
	switch goos {
	case "darwin":
		return OSDarwin
	case "windows":
		return OSWindows
	case "linux":
		if codename, err := readOSReleaseCodename(osReleasePath); err == nil {
			switch codename {
			case string(OSBookworm):
				return OSBookworm
			case string(OSTrixie):
				return OSTrixie
			}
		}
		return OSLinux
	default:
		return OSFlavor(goos)
	}
}

func ResolveLlamaCppArtifact(version string, arch Arch, osFlavor OSFlavor, backend Backend) (ArtifactSpec, error) {
	spec := ArtifactSpec{
		BaseURL: fmt.Sprintf("%s/%s", upstreamReleaseBase, version),
	}

	switch osFlavor {
	case OSLinux:
		switch backend {
		case BackendCPU:
			if arch == ArchARM64 {
				spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cpu-arm64.tar.gz", version)
				return spec, nil
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-x64.tar.gz", version)
		case BackendCUDA:
			spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
			if arch == ArchARM64 {
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-arm64.tar.gz", version)
			} else {
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-13-x64.tar.gz", version)
			}
		case BackendVulkan:
			if arch == ArchARM64 {
				spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-arm64.tar.gz", version)
				return spec, nil
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-x64.tar.gz", version)
		case BackendROCm:
			if arch != ArchAMD64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for Linux ARM64 ROCm are not available")
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-rocm-7.2-x64.tar.gz", version)
		default:
			return ArtifactSpec{}, fmt.Errorf("unsupported backend %q for %s", backend, osFlavor)
		}
	case OSBookworm:
		switch backend {
		case BackendCPU:
			if arch != ArchARM64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for Bookworm AMD64 are not available")
			}
			spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cpu-arm64.tar.gz", version)
		case BackendCUDA:
			if arch != ArchARM64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled CUDA binaries for Bookworm AMD64 are not available")
			}
			spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-arm64.tar.gz", version)
		case BackendVulkan:
			if arch != ArchARM64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled Vulkan binaries for Bookworm AMD64 are not available")
			}
			spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-arm64.tar.gz", version)
		default:
			return ArtifactSpec{}, fmt.Errorf("unsupported backend %q for %s", backend, osFlavor)
		}
	case OSTrixie:
		switch backend {
		case BackendCPU:
			if arch == ArchARM64 {
				spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-trixie-cpu-arm64.tar.gz", version)
				return spec, nil
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-x64.tar.gz", version)
		case BackendCUDA:
			if arch != ArchAMD64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled CUDA binaries for Trixie ARM64 are not available")
			}
			spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-cuda-13-x64.tar.gz", version)
		case BackendVulkan:
			if arch == ArchARM64 {
				spec.BaseURL = fmt.Sprintf("%s/%s", builderReleaseBase, version)
				spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-trixie-vulkan-arm64.tar.gz", version)
				return spec, nil
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-ubuntu-vulkan-x64.tar.gz", version)
		default:
			return ArtifactSpec{}, fmt.Errorf("unsupported backend %q for %s", backend, osFlavor)
		}
	case OSDarwin:
		switch backend {
		case BackendMetal:
			if arch != ArchARM64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for macOS non-ARM64 Metal are not available")
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-macos-arm64.tar.gz", version)
		case BackendCPU:
			if arch == ArchARM64 {
				spec.Filename = fmt.Sprintf("llama-%s-bin-macos-arm64.tar.gz", version)
			} else {
				spec.Filename = fmt.Sprintf("llama-%s-bin-macos-x64.tar.gz", version)
			}
		default:
			return ArtifactSpec{}, fmt.Errorf("unsupported backend %q for %s", backend, osFlavor)
		}
	case OSWindows:
		switch backend {
		case BackendCPU:
			if arch == ArchARM64 {
				spec.Filename = fmt.Sprintf("llama-%s-bin-win-cpu-arm64.zip", version)
			} else {
				spec.Filename = fmt.Sprintf("llama-%s-bin-win-cpu-x64.zip", version)
			}
		case BackendCUDA:
			if arch != ArchAMD64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for Windows ARM64 CUDA are not available")
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-win-cuda-13.1-x64.zip", version)
			spec.AdditionalFiles = []string{"cudart-llama-bin-win-cuda-13.1-x64.zip"}
		case BackendVulkan:
			if arch != ArchAMD64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for Windows ARM64 Vulkan are not available")
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-win-vulkan-x64.zip", version)
		case BackendROCm:
			if arch != ArchAMD64 {
				return ArtifactSpec{}, fmt.Errorf("precompiled binaries for Windows ARM64 ROCm are not available")
			}
			spec.Filename = fmt.Sprintf("llama-%s-bin-win-hip-radeon-x64.zip", version)
		default:
			return ArtifactSpec{}, fmt.Errorf("unsupported backend %q for %s", backend, osFlavor)
		}
	default:
		return ArtifactSpec{}, fmt.Errorf("unsupported operating system %q", osFlavor)
	}

	return spec, nil
}

const defaultOSReleasePath = "/etc/os-release"

func readOSReleaseCodename(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "VERSION_CODENAME=") {
			return strings.Trim(strings.TrimPrefix(line, "VERSION_CODENAME="), `"'`), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("VERSION_CODENAME not found")
}
