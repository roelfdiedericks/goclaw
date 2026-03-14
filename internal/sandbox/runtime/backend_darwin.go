//go:build darwin

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/paths"
	"github.com/roelfdiedericks/goclaw/internal/sandbox/seatbelt"
)

type darwinExecBackend struct{}
type darwinBrowserBackend struct{}

func platformExecBackend() ExecBackend {
	return darwinExecBackend{}
}

func platformBrowserBackend() BrowserBackend {
	return darwinBrowserBackend{}
}

func (darwinExecBackend) Name() string { return "seatbelt" }
func (darwinExecBackend) Available(customPath string) bool {
	return seatbelt.IsAvailable(customPath)
}

func (darwinExecBackend) BuildCommand(command string, opts ExecLaunchOptions) (*exec.Cmd, error) {
	sandboxExec, err := seatbelt.FindSandboxExec(opts.BackendPath)
	if err != nil {
		return nil, err
	}

	profileDir, err := paths.DataPath("seatbelt")
	if err != nil {
		return nil, err
	}
	profilePath, err := seatbelt.WriteProfile(profileDir, "exec", buildExecProfile(opts))
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(sandboxExec, "-f", profilePath, "/bin/bash", "-c", command)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	} else {
		cmd.Dir = opts.WorkspaceDir
	}
	if opts.ClearEnv {
		cmd.Env = BuildMinimalEnv(opts.HomeDir, os.Getenv("PATH"), opts.ExtraEnv)
	} else if len(opts.ExtraEnv) > 0 {
		cmd.Env = mergeEnv(os.Environ(), opts.ExtraEnv)
	}
	return cmd, nil
}

func (darwinBrowserBackend) Name() string { return "seatbelt" }
func (darwinBrowserBackend) Available(customPath string) bool {
	return seatbelt.IsAvailable(customPath)
}

func (darwinBrowserBackend) CreateLauncher(browserBin string, opts BrowserLaunchOptions) (string, error) {
	sandboxExec, err := seatbelt.FindSandboxExec(opts.BackendPath)
	if err != nil {
		return "", err
	}

	profileDir, err := paths.DataPath("seatbelt")
	if err != nil {
		return "", err
	}
	profilePath, err := seatbelt.WriteProfile(profileDir, "browser", buildBrowserProfile(browserBin, opts))
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
	script := "#!/bin/sh\n"
	script += "# GoClaw browser sandbox wrapper (seatbelt)\n\n"
	script += "exec " + ShellQuote(sandboxExec) + " -f " + ShellQuote(profilePath) + " " + ShellQuote(browserBin) + " \"$@\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}
	return wrapperPath, nil
}

func buildExecProfile(opts ExecLaunchOptions) string {
	readRoots := []string{
		"/bin",
		"/usr",
		"/System",
		"/Library",
		"/Applications",
		"/tmp",
		"/private/tmp",
		"/var",
		"/private/var",
		"/dev",
		opts.WorkspaceDir,
	}
	writeRoots := []string{
		opts.WorkspaceDir,
		"/tmp",
		"/private/tmp",
	}
	if opts.HomeDir != "" {
		readRoots = append(readRoots, opts.HomeDir)
		writeRoots = append(writeRoots, opts.HomeDir)
	}
	for _, vol := range opts.Volumes {
		readRoots = append(readRoots, vol.Source)
		writeRoots = append(writeRoots, vol.Source)
	}
	for _, path := range opts.ExtraBind {
		readRoots = append(readRoots, path)
		writeRoots = append(writeRoots, path)
	}
	for _, path := range opts.ExtraRoBind {
		readRoots = append(readRoots, path)
	}

	rules := []string{
		"(version 1)",
		"(deny default)",
		"(allow process*)",
		"(allow sysctl-read)",
		"(allow file-read-metadata)",
		buildSubpathRule("allow file-read*", dedupeRoots(readRoots)),
		buildSubpathRule("allow file-write*", dedupeRoots(writeRoots)),
	}
	if opts.AllowNetwork {
		rules = append(rules, "(allow network-outbound)", "(allow network-inbound)")
	} else {
		rules = append(rules, "(deny network*)")
	}
	return strings.Join(rules, "\n")
}

func buildBrowserProfile(browserBin string, opts BrowserLaunchOptions) string {
	browserBaseDir := filepath.Dir(filepath.Dir(browserBin))
	readRoots := []string{
		"/bin",
		"/usr",
		"/System",
		"/Library",
		"/Applications",
		"/tmp",
		"/private/tmp",
		"/var",
		"/private/var",
		"/dev",
		opts.WorkspaceDir,
		opts.ProfileDir,
		browserBaseDir,
	}
	writeRoots := []string{
		opts.WorkspaceDir,
		opts.ProfileDir,
		"/tmp",
		"/private/tmp",
	}
	if opts.HomeDir != "" {
		readRoots = append(readRoots, opts.HomeDir)
	}
	for _, path := range opts.ExtraBind {
		readRoots = append(readRoots, path)
		writeRoots = append(writeRoots, path)
	}
	for _, path := range opts.ExtraRoBind {
		readRoots = append(readRoots, path)
	}

	rules := []string{
		"(version 1)",
		"(deny default)",
		"(allow process*)",
		"(allow sysctl-read)",
		"(allow file-read-metadata)",
		buildSubpathRule("allow file-read*", dedupeRoots(readRoots)),
		buildSubpathRule("allow file-write*", dedupeRoots(writeRoots)),
		"(allow network-outbound)",
		"(allow network-inbound)",
	}
	return strings.Join(rules, "\n")
}

func buildSubpathRule(verb string, paths []string) string {
	if len(paths) == 0 {
		return fmt.Sprintf("(%s)", verb)
	}
	parts := []string{"(" + verb}
	for _, path := range paths {
		parts = append(parts, fmt.Sprintf(`  (subpath "%s")`, path))
	}
	parts = append(parts, ")")
	return strings.Join(parts, "\n")
}

func dedupeRoots(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		cleaned := filepath.Clean(path)
		if cleaned == "." || cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}
	return out
}

func mergeEnv(base []string, extra map[string]string) []string {
	envMap := map[string]string{}
	for _, entry := range base {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	for key, value := range extra {
		envMap[key] = value
	}
	env := make([]string, 0, len(envMap))
	for key, value := range envMap {
		env = append(env, key+"="+value)
	}
	return env
}
