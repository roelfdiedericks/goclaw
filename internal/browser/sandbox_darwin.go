//go:build darwin

package browser

import (
	"os"
	"path/filepath"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox"
	sbruntime "github.com/roelfdiedericks/goclaw/internal/sandbox/runtime"
)

// BrowserBubblewrapConfig holds sandbox settings for browser sandboxing.
type BrowserBubblewrapConfig struct {
	Enabled     bool
	BwrapPath   string
	ExtraRoBind []string
	ExtraBind   []string
	GPU         bool
}

// IsSandboxAvailable returns true if managed browser sandboxing is available for the backend.
func IsSandboxAvailable(bwrapPath string) bool {
	return sbruntime.BrowserSandboxAvailable(bwrapPath)
}

// CreateSandboxedLauncher creates a wrapper script that launches the browser through the sandbox backend.
func CreateSandboxedLauncher(browserBin, workspace, profileDir string, cfg BrowserBubblewrapConfig) (string, error) {
	if !cfg.Enabled {
		return browserBin, nil
	}

	mgr := sandbox.GetManager()
	policy := mgr.ResolvePolicy()
	extraBind := append([]string{}, cfg.ExtraBind...)
	extraRoBind := append([]string{}, cfg.ExtraRoBind...)
	autoDocsRoots := mgr.GetAutoDocsRoots()
	if mgr.IsAutoDocsWriteMode() {
		extraBind = append(extraBind, autoDocsRoots...)
	} else {
		extraRoBind = append(extraRoBind, autoDocsRoots...)
	}
	return sbruntime.CreateBrowserLauncher(browserBin, sbruntime.BrowserLaunchOptions{
		BackendPath:    cfg.BwrapPath,
		SandboxMode:    mgr.GetMode(),
		WorkspaceDir:   workspace,
		ProfileDir:     profileDir,
		VisibleHomeDir: policy.VisibleHomeDir,
		BackingHomeDir: policy.BackingHomeDir,
		ProtectedDirs:  mgr.GetProtectedDirs(),
		AllowGPU:       cfg.GPU,
		ClearEnv:       true,
		ExtraRoBind:    extraRoBind,
		ExtraBind:      extraBind,
	})
}

// CreatePassthroughLauncher creates a wrapper script that launches the browser with a clean
// environment matching what the sandboxed launcher would roughly provide.
func CreatePassthroughLauncher(browserBin string) (string, error) {
	wrapperDir, err := paths.DataPath("browser-sandbox")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(wrapperDir, 0750); err != nil {
		return "", err
	}

	home, _ := os.UserHomeDir()
	wrapperPath := filepath.Join(wrapperDir, "chromium-wrapper.sh")

	envVars := []string{}
	if path := os.Getenv("PATH"); path != "" {
		envVars = append(envVars, "PATH="+sbruntime.ShellQuote(path))
	}
	envVars = append(envVars, "HOME="+sbruntime.ShellQuote(home))
	envVars = append(envVars, "TERM='xterm'")
	if lang := os.Getenv("LANG"); lang != "" {
		envVars = append(envVars, "LANG="+sbruntime.ShellQuote(lang))
	} else {
		envVars = append(envVars, "LANG='C.UTF-8'")
	}
	if user := os.Getenv("USER"); user != "" {
		envVars = append(envVars, "USER="+sbruntime.ShellQuote(user))
	}

	script := "#!/bin/sh\n"
	script += "# GoClaw browser wrapper (clean environment, no sandbox)\n"
	script += "exec env -i \\\n"
	for i, env := range envVars {
		script += "  " + env
		if i < len(envVars)-1 {
			script += " \\\n"
		}
	}
	script += " \\\n  " + sbruntime.ShellQuote(browserBin) + " \"$@\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}

	L_debug("browser: created passthrough wrapper", "wrapper", wrapperPath, "browser", browserBin)
	return wrapperPath, nil
}

// CleanupSandboxWrapper removes the sandbox wrapper script.
func CleanupSandboxWrapper() {
	wrapperPath, err := paths.DataPath("browser-sandbox/chromium-wrapper.sh")
	if err != nil {
		return
	}
	_ = os.Remove(wrapperPath)
}

// CheckBwrapForBrowser returns true if sandboxing should be enabled.
func CheckBwrapForBrowser(cfg BrowserBubblewrapConfig) bool {
	if !cfg.Enabled {
		return false
	}

	if !sbruntime.BrowserSandboxAvailable(cfg.BwrapPath) {
		L_warn("browser sandbox: managed sandbox backend unavailable, running unsandboxed",
			"backend", sbruntime.SandboxBackendName())
		return false
	}

	L_info("browser sandbox: backend enabled", "backend", sbruntime.SandboxBackendName(), "gpu", cfg.GPU)
	return true
}
