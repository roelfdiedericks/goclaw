// Package sandbox provides secure file operations with path validation.
// Matches OpenClaw's sandbox-paths.ts behavior for security parity.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// In "home" mode:
//   - ~ expands to the sandbox home directory
//   - Absolute paths under the real home are rewritten to the sandbox home
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
// In home-like modes, ~ paths usually expand to the sandbox home directory and
// autodocs roots remain mapped to the real home.
func (m *Manager) ValidatePath(inputPath, workingDir string) (string, error) {
	autoDocsRoots := m.GetAutoDocsRoots()
	expanded := m.expandManagedPath(inputPath, autoDocsRoots)

	var resolved string
	if filepath.IsAbs(expanded) {
		resolved = filepath.Clean(expanded)
	} else {
		resolved = filepath.Clean(filepath.Join(workingDir, expanded))
	}

	workspaceResolved := filepath.Clean(m.workspaceRoot)
	rootUsed, relative, ok := selectSandboxRoot(resolved, workspaceResolved, m.homeDir, autoDocsRoots)
	if !ok {
		homeResolved := ""
		if m.homeDir != "" {
			homeResolved = filepath.Clean(m.homeDir)
		}
		L_warn("sandbox: path escapes allowed roots", "path", inputPath, "resolved", resolved, "workspace", workspaceResolved, "home", homeResolved)
		return "", fmt.Errorf("path escapes sandbox root: %s", inputPath)
	}

	if relative != "" && relative != "." {
		if err := assertNoSymlink(relative, rootUsed); err != nil {
			return "", err
		}
	}

	filename := filepath.Base(resolved)
	for _, denied := range deniedFiles {
		if filename == denied {
			L_warn("sandbox: access to denied file blocked", "path", inputPath, "file", denied)
			return "", fmt.Errorf("access denied: %s is a protected file", denied)
		}
	}

	L_trace("sandbox: path validated", "input", inputPath, "resolved", resolved, "relative", relative)
	return resolved, nil
}

func (m *Manager) expandManagedPath(filePath string, autoDocsRoots []string) string {
	normalized := normalizeUnicodeSpaces(filePath)
	realHome, _ := os.UserHomeDir()

	targetHome := m.homeDir
	if targetHome == "" {
		targetHome = realHome
	}

	if normalized == "~" {
		return targetHome
	}
	if strings.HasPrefix(normalized, "~/") {
		realCandidate := filepath.Clean(filepath.Join(realHome, normalized[2:]))
		if pathWithinAnyRoot(realCandidate, autoDocsRoots) {
			return realCandidate
		}
		return targetHome + normalized[1:]
	}

	if m.homeDir != "" && realHome != "" {
		if normalized == realHome {
			return m.homeDir
		}
		if strings.HasPrefix(normalized, realHome+"/") {
			cleaned := filepath.Clean(normalized)
			if pathWithinAnyRoot(cleaned, autoDocsRoots) {
				return cleaned
			}
			rewritten := m.homeDir + cleaned[len(realHome):]
			L_debug("sandbox: rewriting home path", "original", normalized, "rewritten", rewritten)
			return rewritten
		}
	}

	return normalized
}

func selectSandboxRoot(resolved string, workspaceRoot string, sandboxHome string, autoDocsRoots []string) (string, string, bool) {
	roots := []string{workspaceRoot}
	if sandboxHome != "" {
		roots = append(roots, filepath.Clean(sandboxHome))
	}
	roots = append(roots, autoDocsRoots...)

	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		relative, err := filepath.Rel(cleanRoot, resolved)
		if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
			continue
		}
		return cleanRoot, relative, true
	}
	return "", "", false
}

func pathWithinAnyRoot(path string, roots []string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
	resolved, err := m.ValidatePath(inputPath, workingDir)
	if err != nil {
		return "", err
	}

	autoDocsRoots := m.GetAutoDocsRoots()
	workspaceResolved := filepath.Clean(m.workspaceRoot)
	rootUsed, relative, ok := selectSandboxRoot(resolved, workspaceResolved, m.homeDir, autoDocsRoots)
	if !ok {
		return "", fmt.Errorf("write denied: path escapes sandbox roots")
	}

	if rootUsed == workspaceResolved {
		if m.IsPathProtected(relative) {
			L_warn("sandbox: write to protected directory blocked", "path", inputPath, "relative", relative)
			return "", fmt.Errorf("write denied: path is in a protected directory")
		}
		return resolved, nil
	}

	if m.homeDir != "" && rootUsed == filepath.Clean(m.homeDir) {
		return resolved, nil
	}

	if pathWithinAnyRoot(resolved, autoDocsRoots) {
		if m.IsAutoDocsWriteMode() {
			return resolved, nil
		}
		return "", fmt.Errorf("write denied: autodocs mode is read-only")
	}

	return "", fmt.Errorf("write denied: path is outside writable sandbox roots")
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
