package localllm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type SystemProfile struct {
	OSFlavor          OSFlavor
	Arch              Arch
	TotalRAMBytes     uint64
	AvailableBackends []Backend
	Recommended       Backend
}

var (
	execCommand     = exec.Command
	lookPath        = exec.LookPath
	procMemInfoPath = "/proc/meminfo"
	detectProfileMu sync.Mutex
	lastProfileKey  string
)

func DetectSystemProfile() SystemProfile {
	profile := SystemProfile{
		OSFlavor: CurrentOSFlavor(),
		Arch:     CurrentArch(),
	}

	ram, err := detectTotalRAMBytes()
	if err != nil {
		L_warn("localllm: failed to detect system ram", "error", err)
	} else {
		profile.TotalRAMBytes = ram
	}

	profile.AvailableBackends = detectAvailableBackends(profile.OSFlavor, profile.Arch)
	profile.Recommended = recommendBackend(profile.OSFlavor, profile.AvailableBackends)

	logDetectedSystemProfile(profile)

	return profile
}

func logDetectedSystemProfile(profile SystemProfile) {
	profileKey := fmt.Sprintf("%s|%s|%d|%v|%s",
		profile.OSFlavor,
		profile.Arch,
		profile.TotalRAMBytes,
		profile.AvailableBackends,
		profile.Recommended,
	)

	detectProfileMu.Lock()
	changed := profileKey != lastProfileKey
	lastProfileKey = profileKey
	detectProfileMu.Unlock()

	logArgs := []any{
		"os", profile.OSFlavor,
		"arch", profile.Arch,
		"ramBytes", profile.TotalRAMBytes,
		"availableBackends", profile.AvailableBackends,
		"recommendedBackend", profile.Recommended,
	}
	if changed {
		L_info("localllm: detected system profile", logArgs...)
		return
	}
	L_debug("localllm: detected system profile", logArgs...)
}

func detectAvailableBackends(osFlavor OSFlavor, arch Arch) []Backend {
	backends := []Backend{BackendCPU}

	switch osFlavor {
	case OSDarwin:
		if arch == ArchARM64 {
			backends = append(backends, BackendMetal)
		}
	case OSLinux, OSBookworm, OSTrixie, OSWindows:
		if ok, version := probeCUDA(); ok {
			L_debug("localllm: cuda detected", "version", version)
			backends = append(backends, BackendCUDA)
		}
		if ok, version := probeROCm(); ok {
			L_debug("localllm: rocm detected", "version", version)
			backends = append(backends, BackendROCm)
		}
		if ok, version := probeVulkan(); ok {
			L_debug("localllm: vulkan detected", "version", version)
			backends = append(backends, BackendVulkan)
		}
	}

	slices.Sort(backends)
	return slices.Compact(backends)
}

func recommendBackend(osFlavor OSFlavor, available []Backend) Backend {
	preferred := []Backend{BackendCUDA, BackendROCm, BackendMetal, BackendVulkan, BackendCPU}
	if osFlavor == OSDarwin {
		preferred = []Backend{BackendMetal, BackendCPU}
	}

	for _, candidate := range preferred {
		if slices.Contains(available, candidate) {
			return candidate
		}
	}
	return BackendCPU
}

func detectTotalRAMBytes() (uint64, error) {
	switch runtime.GOOS {
	case "linux":
		return readLinuxMemTotal(procMemInfoPath)
	case "darwin":
		return readCommandUint64("sysctl", "-n", "hw.memsize")
	case "windows":
		return readCommandUint64("powershell", "-NoProfile", "-Command", "[int64](Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory")
	default:
		return 0, fmt.Errorf("unsupported operating system %q", runtime.GOOS)
	}
}

func readLinuxMemTotal(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed MemTotal line %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse MemTotal: %w", err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("MemTotal not found")
}

func readCommandUint64(name string, args ...string) (uint64, error) {
	cmd := execCommand(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	value := strings.TrimSpace(out.String())
	value = strings.Trim(value, "\r\n\t ")
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s output %q: %w", name, value, err)
	}
	return n, nil
}

func probeCUDA() (bool, string) {
	if runtime.GOOS == "darwin" {
		return false, ""
	}
	out, err := runProbe("nvidia-smi")
	if err != nil {
		return false, ""
	}
	return true, firstMatch(out, `CUDA Version:\s*([0-9.]+)`)
}

func probeROCm() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, ""
	}
	out, err := runProbe("rocminfo")
	if err != nil {
		return false, ""
	}
	return true, firstMatch(out, `Runtime Version:\s*([0-9.]+)`)
}

func probeVulkan() (bool, string) {
	if _, err := lookPath("vulkaninfo"); err != nil {
		return false, ""
	}
	out, err := runProbe("vulkaninfo", "--summary")
	if err != nil {
		return false, ""
	}
	return true, firstMatch(out, `Vulkan Instance Version:\s*([0-9.]+)`)
}

func runProbe(name string, args ...string) (string, error) {
	cmd := execCommand(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func firstMatch(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(s)
	if len(match) >= 2 {
		return match[1]
	}
	return ""
}
