package runtime

import (
	"fmt"
	"os"
	"os/exec"
)

type SandboxVolume struct {
	MountPoint string
	Source     string
}

// ExecLaunchOptions describes a sandboxed exec launch without exposing backend details.
type ExecLaunchOptions struct {
	BackendPath   string
	WorkspaceDir  string
	WorkDir       string
	HomeDir       string
	Volumes       []SandboxVolume
	ProtectedDirs []string
	ClearEnv      bool
	AllowNetwork  bool
	ExtraEnv      map[string]string
	ExtraBind     []string
	ExtraRoBind   []string
}

// BrowserLaunchOptions describes a managed browser launch without exposing backend details.
type BrowserLaunchOptions struct {
	BackendPath   string
	WorkspaceDir  string
	ProfileDir    string
	HomeDir       string
	ProtectedDirs []string
	Headless      bool
	AllowGPU      bool
	ClearEnv      bool
	ExtraRoBind   []string
	ExtraBind     []string
}

type ExecBackend interface {
	Name() string
	Available(customPath string) bool
	BuildCommand(command string, opts ExecLaunchOptions) (*exec.Cmd, error)
}

type BrowserBackend interface {
	Name() string
	Available(customPath string) bool
	CreateLauncher(browserBin string, opts BrowserLaunchOptions) (string, error)
}

func currentExecBackend() ExecBackend {
	return platformExecBackend()
}

func currentBrowserBackend() BrowserBackend {
	return platformBrowserBackend()
}

func ExecSandboxAvailable(customPath string) bool {
	return currentExecBackend().Available(customPath)
}

func BrowserSandboxAvailable(customPath string) bool {
	return currentBrowserBackend().Available(customPath)
}

func SandboxBackendName() string {
	return currentExecBackend().Name()
}

func BuildExecCommand(command string, opts ExecLaunchOptions) (*exec.Cmd, error) {
	backend := currentExecBackend()
	if !backend.Available(opts.BackendPath) {
		return nil, nil
	}
	return backend.BuildCommand(command, opts)
}

func CreateBrowserLauncher(browserBin string, opts BrowserLaunchOptions) (string, error) {
	backend := currentBrowserBackend()
	if !backend.Available(opts.BackendPath) {
		return "", fmt.Errorf("%s sandbox backend not available", backend.Name())
	}
	return backend.CreateLauncher(browserBin, opts)
}

func BuildMinimalEnv(homeDir, pathValue string, extraEnv map[string]string) []string {
	envMap := map[string]string{
		"HOME": homeDir,
		"PATH": pathValue,
		"TERM": "xterm",
	}

	if lang := os.Getenv("LANG"); lang != "" {
		envMap["LANG"] = lang
	} else {
		envMap["LANG"] = "C.UTF-8"
	}
	if user := os.Getenv("USER"); user != "" {
		envMap["USER"] = user
	}

	for key, value := range extraEnv {
		envMap[key] = value
	}

	env := make([]string, 0, len(envMap))
	for key, value := range envMap {
		env = append(env, key+"="+value)
	}
	return env
}
