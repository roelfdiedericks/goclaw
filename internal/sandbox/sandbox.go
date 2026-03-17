// Package sandbox provides secure file operations with path validation.
// Matches OpenClaw's sandbox-paths.ts behavior for security parity.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Unicode spaces that should be normalized to regular space
var unicodeSpaces = regexp.MustCompile(`[\x{00A0}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)

// Denied files - blocked even within the sandbox.
var deniedFiles = []string{
	"users.json",
	"goclaw.json",
	"openclaw.json",
	".env",
	".env.local",
	".env.production",
	"id_rsa",
	"id_ed25519",
	".gitconfig",
}

// defaultWriteProtectedDirs are the base directories protected by default.
var defaultWriteProtectedDirs = []string{
	"skills",
	//"media",
}

func normalizeUnicodeSpaces(s string) string {
	return unicodeSpaces.ReplaceAllString(s, " ")
}

// expandSandboxPath handles ~ expansion and unicode normalization for file tool paths.
// In sandbox-home remap environments, absolute paths under real home are rewritten
// to the sandbox home backing path.
func expandSandboxPath(filePath string, sandboxHomeDir string) string {
	normalized := normalizeUnicodeSpaces(filePath)

	// Get real home for comparison
	realHome, _ := os.UserHomeDir()

	// Determine target home directory
	targetHome := sandboxHomeDir
	if targetHome == "" {
		targetHome = realHome
	}

	// Handle ~ and ~/
	if normalized == "~" {
		return targetHome
	}
	if strings.HasPrefix(normalized, "~/") {
		return targetHome + normalized[1:]
	}

	// In home mode, rewrite absolute paths under real home to sandbox home
	if sandboxHomeDir != "" && realHome != "" {
		if normalized == realHome {
			return sandboxHomeDir
		}
		if strings.HasPrefix(normalized, realHome+"/") {
			rewritten := sandboxHomeDir + normalized[len(realHome):]
			L_debug("sandbox: rewriting home path", "original", normalized, "rewritten", rewritten)
			return rewritten
		}
	}

	return normalized
}

// ValidatePath validates that a path is within allowed roots and contains no symlinks.
// HOME path behavior is policy-driven and may differ by platform/mode.
func (m *Manager) ValidatePath(inputPath, workingDir string) (string, error) {
	policy := m.ResolvePolicy()
	resolution, err := policy.ResolvePath(inputPath, workingDir)
	if err != nil {
		L_warn("sandbox: path escapes allowed roots", "path", inputPath, "error", err, "workspace", policy.VisibleWorkspace, "home", policy.VisibleHomeDir)
		return "", fmt.Errorf("path escapes sandbox root: %s", inputPath)
	}
	if shouldDenyDarwinHiddenHomePath(policy, resolution) {
		return "", fmt.Errorf("access denied: hidden home paths are blocked in darwin home mode")
	}

	if resolution.Relative != "" && resolution.Relative != "." {
		rootForSymlink := resolution.RootPath
		if resolution.RootKind == RootSandboxHome && policy.BackingHomeDir != "" {
			rootForSymlink = policy.BackingHomeDir
		}
		if err := assertNoSymlink(resolution.Relative, rootForSymlink); err != nil {
			return "", err
		}
	}

	filename := filepath.Base(resolution.ActualPath)
	for _, denied := range deniedFiles {
		if filename == denied {
			L_warn("sandbox: access to denied file blocked", "path", inputPath, "file", denied)
			return "", fmt.Errorf("access denied: %s is a protected file", denied)
		}
	}

	L_trace("sandbox: path validated", "input", inputPath, "visible", resolution.VisiblePath, "resolved", resolution.ActualPath, "relative", resolution.Relative, "rootKind", resolution.RootKind)
	return resolution.ActualPath, nil
}

func assertNoSymlink(relative, root string) error {
	parts := strings.Split(relative, string(filepath.Separator))
	current := root

	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)

		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat path component: %w", err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			L_warn("sandbox: symlink detected in path", "path", current)
			return fmt.Errorf("symlink not allowed in sandbox path: %s", current)
		}
	}

	return nil
}

// ReadFile validates the path and reads the file contents.
func (m *Manager) ReadFile(inputPath, workingDir string) ([]byte, error) {
	resolved, err := m.ValidatePath(inputPath, workingDir)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return content, nil
}

// AtomicWriteFile writes data to a file atomically (write to temp, then rename).
func (m *Manager) AtomicWriteFile(path string, data []byte, defaultPerm os.FileMode) error {
	perm := defaultPerm
	if perm == 0 {
		perm = 0600
	}

	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
		L_trace("sandbox: preserving file permissions", "path", path, "perm", perm)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".goclaw-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}

	success = true
	return nil
}

// ValidateWritePath validates a path for write operations.
// Blocks writes to protected directories and autodocs roots when in read-only mode.
func (m *Manager) ValidateWritePath(inputPath, workingDir string) (string, error) {
	policy := m.ResolvePolicy()
	resolution, err := policy.ResolvePath(inputPath, workingDir)
	if err != nil {
		return "", err
	}
	if shouldDenyDarwinHiddenHomePath(policy, resolution) {
		return "", fmt.Errorf("write denied: hidden home paths are blocked in darwin home mode")
	}

	filename := filepath.Base(resolution.ActualPath)
	for _, denied := range deniedFiles {
		if filename == denied {
			return "", fmt.Errorf("access denied: %s is a protected file", denied)
		}
	}

	switch resolution.RootKind {
	case RootWorkspace:
		if m.IsPathProtected(resolution.Relative) {
			L_warn("sandbox: write to protected directory blocked", "path", inputPath, "relative", resolution.Relative)
			return "", fmt.Errorf("write denied: path is in a protected directory")
		}
		return resolution.ActualPath, nil
	case RootSandboxHome:
		return resolution.ActualPath, nil
	case RootAutoDocs:
		if policy.AutoDocsWrite {
			return resolution.ActualPath, nil
		}
		return "", fmt.Errorf("write denied: autodocs mode is read-only")
	default:
		return "", fmt.Errorf("write denied: path is outside writable sandbox roots")
	}
}

func shouldDenyDarwinHiddenHomePath(policy ResolvedPolicy, resolution ResolvedPath) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if policy.Mode != ModeHome {
		return false
	}
	if resolution.RootKind != RootSandboxHome {
		return false
	}
	return isHiddenRelativePath(resolution.Relative)
}

func isHiddenRelativePath(relative string) bool {
	clean := filepath.Clean(relative)
	if clean == "." || clean == "" {
		return false
	}
	first := clean
	if idx := strings.IndexRune(clean, filepath.Separator); idx >= 0 {
		first = clean[:idx]
	}
	return strings.HasPrefix(first, ".")
}

// WriteFileValidated validates the path for writes, then writes atomically.
func (m *Manager) WriteFileValidated(inputPath, workingDir string, data []byte, defaultPerm os.FileMode) error {
	resolved, err := m.ValidateWritePath(inputPath, workingDir)
	if err != nil {
		return err
	}

	return m.AtomicWriteFile(resolved, data, defaultPerm)
}

// shortPath shortens a path for display by replacing the home directory with ~.
// In home mode, also handles the sandbox home directory.
func (m *Manager) shortPath(value string) string {
	// Try sandbox home first (if in home mode)
	if m.homeDir != "" {
		sandboxHome := filepath.Clean(m.homeDir)
		if strings.HasPrefix(value, sandboxHome) {
			return "~" + value[len(sandboxHome):]
		}
	}

	// Fall back to real home
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	if strings.HasPrefix(value, home) {
		return "~" + value[len(home):]
	}
	return value
}
