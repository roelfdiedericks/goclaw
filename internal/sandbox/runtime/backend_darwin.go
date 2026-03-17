//go:build darwin

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
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
	if err := validateDarwinSandboxMode(opts.SandboxMode); err != nil {
		return nil, err
	}
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

	L_debug("seatbelt exec: building command",
		"backend", "seatbelt",
		"sandboxExec", sandboxExec,
		"profilePath", profilePath,
		"workspaceDir", opts.WorkspaceDir,
		"workDir", opts.WorkDir,
		"visibleHomeDir", opts.VisibleHomeDir,
		"backingHomeDir", opts.BackingHomeDir,
		"volumes", len(opts.Volumes),
		"protectedDirs", len(opts.ProtectedDirs),
		"clearEnv", opts.ClearEnv,
		"allowNetwork", opts.AllowNetwork,
	)

	wrapperPath, err := writeExecWrapper(sandboxExec, profilePath, command)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(wrapperPath)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	} else {
		cmd.Dir = opts.WorkspaceDir
	}
	if opts.ClearEnv {
		pathValue := opts.PathValue
		if pathValue == "" {
			pathValue = os.Getenv("PATH")
		}
		cmd.Env = BuildMinimalEnv(opts.VisibleHomeDir, pathValue, opts.ExtraEnv)
	} else if len(opts.ExtraEnv) > 0 {
		cmd.Env = mergeEnv(os.Environ(), opts.ExtraEnv)
	}
	L_debug("seatbelt exec: command prepared",
		"dir", cmd.Dir,
		"argv", cmd.Args,
		"envLen", len(cmd.Env),
	)
	return cmd, nil
}

func (darwinBrowserBackend) Name() string { return "seatbelt" }
func (darwinBrowserBackend) Available(customPath string) bool {
	return seatbelt.IsAvailable(customPath)
}

func (darwinBrowserBackend) CreateLauncher(browserBin string, opts BrowserLaunchOptions) (string, error) {
	if err := validateDarwinSandboxMode(opts.SandboxMode); err != nil {
		return "", err
	}
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
	script += "PROFILE_PATH=" + ShellQuote(profilePath) + "\n"
	script += "cleanup() {\n"
	script += "  rm -f \"$PROFILE_PATH\"\n"
	script += "}\n"
	script += "trap cleanup EXIT INT TERM\n\n"
	script += ShellQuote(sandboxExec) + " -f \"$PROFILE_PATH\" " + ShellQuote(browserBin) + " \"$@\"\n"
	script += "status=$?\n"
	script += "exit \"$status\"\n"

	//nolint:gosec // G306: Executable script needs execute permission
	if err := os.WriteFile(wrapperPath, []byte(script), 0750); err != nil {
		return "", err
	}
	return wrapperPath, nil
}

func writeExecWrapper(sandboxExec string, profilePath string, command string) (string, error) {
	wrapperDir, err := paths.DataPath("seatbelt")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(wrapperDir, 0750); err != nil {
		return "", err
	}

	file, err := os.CreateTemp(wrapperDir, "exec-wrapper-*.sh")
	if err != nil {
		return "", err
	}
	defer file.Close()

	script := "#!/bin/sh\n"
	script += "# GoClaw exec sandbox wrapper (seatbelt)\n\n"
	script += "PROFILE_PATH=" + ShellQuote(profilePath) + "\n"
	script += "cleanup() {\n"
	script += "  rm -f \"$PROFILE_PATH\" \"$0\"\n"
	script += "}\n"
	script += "trap cleanup EXIT INT TERM\n\n"
	script += ShellQuote(sandboxExec) + " -f \"$PROFILE_PATH\" /bin/bash -c " + ShellQuote(command) + "\n"
	script += "status=$?\n"
	script += "exit \"$status\"\n"

	if _, err := file.WriteString(script); err != nil {
		return "", err
	}
	if err := file.Chmod(0750); err != nil {
		return "", err
	}

	return filepath.Clean(file.Name()), nil
}

func validateDarwinSandboxMode(mode string) error {
	if mode == "volumes" {
		return fmt.Errorf("darwin seatbelt sandbox does not support volumes mode; use home, autodocs-read, or autodocs-write instead")
	}
	if mode == "ephemeral" {
		return fmt.Errorf("darwin seatbelt sandbox does not support ephemeral mode; use home, autodocs-read, or autodocs-write instead")
	}
	return nil
}

func buildExecProfile(opts ExecLaunchOptions) string {
	readRoots := []string{
		opts.WorkspaceDir,
		"/tmp",
		"/private/tmp",
	}
	writeRoots := []string{
		opts.WorkspaceDir,
		"/tmp",
		"/private/tmp",
	}
	if opts.WorkDir != "" {
		readRoots = append(readRoots, opts.WorkDir)
		writeRoots = append(writeRoots, opts.WorkDir)
	}
	if opts.BackingHomeDir != "" {
		readRoots = append(readRoots, opts.BackingHomeDir)
		writeRoots = append(writeRoots, opts.BackingHomeDir)
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
	}
	rules = append(rules, buildExecReadRules(opts, dedupeRoots(readRoots))...)
	rules = append(rules, buildWriteRules(dedupeRoots(writeRoots), dedupeRoots(opts.ProtectedDirs))...)
	rules = append(rules, buildRuntimeDeviceWriteRules()...)
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
	if opts.BackingHomeDir != "" {
		readRoots = append(readRoots, opts.BackingHomeDir)
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
	}
	rules = append(rules, buildWriteRules(dedupeRoots(writeRoots), dedupeRoots(opts.ProtectedDirs))...)
	rules = append(rules,
		"(allow network-outbound)",
		"(allow network-inbound)",
	)
	return strings.Join(rules, "\n")
}

func buildExecReadRules(opts ExecLaunchOptions, allowRoots []string) []string {
	realHome := filepath.Clean(opts.VisibleHomeDir)
	if realHome == "" || realHome == "." {
		realHome, _ = os.UserHomeDir()
	}
	if shouldDenyRealHome(realHome, opts.BackingHomeDir) {
		return []string{
			"(allow file-read*)",
			buildSubpathRule("deny file-read*", []string{filepath.Clean(realHome)}),
			buildLiteralRule("allow file-read*", filepath.Clean(realHome)),
			buildSubpathRule("allow file-read*", allowRoots),
		}
	}

	// Darwin seatbelt read confinement is too brittle for normal process launch
	// unless we can redirect HOME to a separate sandbox backing directory.
	return []string{"(allow file-read*)"}
}

func shouldDenyRealHome(realHome string, sandboxHome string) bool {
	if realHome == "" || sandboxHome == "" {
		return false
	}
	return filepath.Clean(realHome) != filepath.Clean(sandboxHome)
}

func buildRuntimeDeviceWriteRules() []string {
	return []string{
		"(allow file-write*",
		"  (require-all",
		`    (literal "/dev/null")`,
		"  )",
		")",
	}
}

func buildLiteralRule(verb string, path string) string {
	return strings.Join([]string{
		"(" + verb,
		"  (require-all",
		fmt.Sprintf(`    (literal "%s")`, path),
		"  )",
		")",
	}, "\n")
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

func buildWriteRules(writeRoots []string, protectedDirs []string) []string {
	if len(writeRoots) == 0 {
		return []string{"(allow file-write*)"}
	}

	rules := make([]string, 0, len(writeRoots))
	for _, root := range writeRoots {
		if isProtectedRoot(root, protectedDirs) {
			continue
		}
		parts := []string{"(allow file-write*", "  (require-all", fmt.Sprintf(`    (subpath "%s")`, root)}
		for _, protected := range protectedDirs {
			if isNestedPath(protected, root) {
				parts = append(parts, fmt.Sprintf(`    (require-not (subpath "%s"))`, protected))
			}
		}
		parts = append(parts, "  )", ")")
		rules = append(rules, strings.Join(parts, "\n"))
	}
	return rules
}

func isProtectedRoot(root string, protectedDirs []string) bool {
	for _, protected := range protectedDirs {
		if isNestedPath(root, protected) {
			return true
		}
	}
	return false
}

func isNestedPath(path string, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(filepath.Separator))
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
