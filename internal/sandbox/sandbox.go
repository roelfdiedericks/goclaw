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
	"media",
}

func normalizeUnicodeSpaces(s string) string {
	return unicodeSpaces.ReplaceAllString(s, " ")
}

// expandSandboxPath handles ~ expansion and unicode normalization for file tool paths.
func expandSandboxPath(filePath string) string {
	normalized := normalizeUnicodeSpaces(filePath)

	if normalized == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(normalized, "~/") {
		home, _ := os.UserHomeDir()
		return home + normalized[1:]
	}
	return normalized
}

// ValidatePath validates that a path is within the workspace root and contains no symlinks.
// workspaceRoot is taken from the manager.
func (m *Manager) ValidatePath(inputPath, workingDir string) (string, error) {
	expanded := expandSandboxPath(inputPath)

	var resolved string
	if filepath.IsAbs(expanded) {
		resolved = filepath.Clean(expanded)
	} else {
		resolved = filepath.Clean(filepath.Join(workingDir, expanded))
	}

	rootResolved := filepath.Clean(m.workspaceRoot)

	relative, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return "", fmt.Errorf("failed to compute relative path: %w", err)
	}

	if relative == "" {
		// Path is exactly the root - allowed
	} else if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		L_warn("sandbox: path escapes workspace", "path", inputPath, "resolved", resolved, "root", rootResolved)
		return "", fmt.Errorf("path escapes sandbox root (%s): %s", shortPath(rootResolved), inputPath)
	}

	if relative != "" && relative != "." {
		if err := assertNoSymlink(relative, rootResolved); err != nil {
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
// Blocks writes to protected directories.
func (m *Manager) ValidateWritePath(inputPath, workingDir string) (string, error) {
	resolved, err := m.ValidatePath(inputPath, workingDir)
	if err != nil {
		return "", err
	}

	rootResolved := filepath.Clean(m.workspaceRoot)
	relative, _ := filepath.Rel(rootResolved, resolved)

	if m.IsPathProtected(relative) {
		L_warn("sandbox: write to protected directory blocked", "path", inputPath, "relative", relative)
		return "", fmt.Errorf("write denied: path is in a protected directory")
	}

	return resolved, nil
}

// WriteFileValidated validates the path for writes, then writes atomically.
func (m *Manager) WriteFileValidated(inputPath, workingDir string, data []byte, defaultPerm os.FileMode) error {
	resolved, err := m.ValidateWritePath(inputPath, workingDir)
	if err != nil {
		return err
	}

	return m.AtomicWriteFile(resolved, data, defaultPerm)
}

func shortPath(value string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	if strings.HasPrefix(value, home) {
		return "~" + value[len(home):]
	}
	return value
}
