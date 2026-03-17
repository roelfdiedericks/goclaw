//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox/bwrap"
)

type linuxExecBackend struct{}
type linuxBrowserBackend struct{}

func platformExecBackend() ExecBackend {
	return linuxExecBackend{}
}

func platformBrowserBackend() BrowserBackend {
	return linuxBrowserBackend{}
}

func (linuxExecBackend) Name() string { return "bubblewrap" }

func (linuxExecBackend) Available(customPath string) bool {
	return bwrap.IsLinux() && bwrap.IsAvailable(customPath)
}

func (linuxExecBackend) BuildCommand(command string, opts ExecLaunchOptions) (*exec.Cmd, error) {
	b := bwrap.ExecSandbox(opts.WorkspaceDir, opts.VisibleHomeDir, opts.BackingHomeDir, opts.SandboxMode, opts.AllowNetwork, opts.ClearEnv)
	if opts.BackendPath != "" {
		b.BwrapPath(opts.BackendPath)
	}
	for _, path := range opts.ExtraRoBind {
		b.RoBind(path)
	}
	for _, path := range opts.ExtraBind {
		b.Bind(path)
	}
	for key, value := range opts.ExtraEnv {
		b.SetEnv(key, value)
	}
	if opts.WorkDir != "" && opts.WorkDir != opts.WorkspaceDir {
		b.Bind(opts.WorkDir)
		b.Chdir(opts.WorkDir)
	}
	b.ShellCommand(command)
	return b.BuildCommand()
}

func (linuxBrowserBackend) Name() string { return "bubblewrap" }

func (linuxBrowserBackend) Available(customPath string) bool {
	return bwrap.IsLinux() && bwrap.IsAvailable(customPath)
}

func (linuxBrowserBackend) CreateLauncher(browserBin string, opts BrowserLaunchOptions) (string, error) {
	b := bwrap.BrowserSandbox(opts.WorkspaceDir, opts.ProfileDir, opts.VisibleHomeDir, opts.BackingHomeDir, opts.SandboxMode, opts.AllowGPU)
	if opts.BackendPath != "" {
		b.BwrapPath(opts.BackendPath)
	}

	browserBinDir := filepath.Dir(browserBin)
	browserBaseDir := filepath.Dir(browserBinDir)
	b.RoBind(browserBaseDir)

	for _, path := range opts.ExtraRoBind {
		b.RoBind(path)
	}
	for _, path := range opts.ExtraBind {
		b.Bind(path)
	}

	b.Command(browserBin)
	bwrapPath, args, err := b.Build()
	if err != nil {
		return "", err
	}

	wrapperDir, err := paths.DataPath("browser-sandbox")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(wrapperDir, 0750); err != nil {
		return "", err
	}

	wrapperPath := filepath.Join(wrapperDir, "chromium-wrapper.sh")
	baseArgs := args[:len(args)-1]

	script := "#!/bin/sh\n"
	script += "# GoClaw browser sandbox wrapper\n"
	script += "# This script runs Chromium through bubblewrap for sandboxing\n\n"
	script += "exec " + ShellQuote(bwrapPath)
	for _, arg := range baseArgs {
		script += " " + ShellQuote(arg)
	}
	script += " " + ShellQuote(browserBin) + " \"$@\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}
	return wrapperPath, nil
}
