//go:build !linux && !darwin

package runtime

import "os/exec"

type noopExecBackend struct{}
type noopBrowserBackend struct{}

func platformExecBackend() ExecBackend {
	return noopExecBackend{}
}

func platformBrowserBackend() BrowserBackend {
	return noopBrowserBackend{}
}

func (noopExecBackend) Name() string                     { return "none" }
func (noopExecBackend) Available(customPath string) bool { return false }
func (noopExecBackend) BuildCommand(command string, opts ExecLaunchOptions) (*exec.Cmd, error) {
	return nil, nil
}

func (noopBrowserBackend) Name() string                     { return "none" }
func (noopBrowserBackend) Available(customPath string) bool { return false }
func (noopBrowserBackend) CreateLauncher(browserBin string, opts BrowserLaunchOptions) (string, error) {
	return "", nil
}
