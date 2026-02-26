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
// Matches OpenClaw's UNICODE_SPACES pattern
var unicodeSpaces = regexp.MustCompile(`[\x{00A0}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)

// Denied files - these are blocked even within the sandbox.
// Protects sensitive config that might exist in a development workspace.
// Its not super useful, but reasonable and cheap to do.
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

// Write-protected directories - agent can read but not write to these
var writeProtectedDirs = []string{
	"skills",
	"media",
}

// normalizeUnicodeSpaces replaces unicode space characters with regular spaces
func normalizeUnicodeSpaces(s string) string {
	return unicodeSpaces.ReplaceAllString(s, " ")
}

// expandPath handles ~ expansion and unicode normalization
func expandPath(filePath string) string {
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
// Returns the resolved absolute path if valid.
//
// Parameters:
//   - inputPath: the path provided by the agent (can be relative or absolute)
//   - workingDir: the current working directory for relative path resolution
//   - workspaceRoot: the root directory that paths must stay within
//
// Security checks (matching OpenClaw sandbox-paths.ts):
//  1. Unicode space normalization
//  2. Path must resolve within workspaceRoot (no .. escapes)
//  3. No symlinks in path that could escape sandbox
func ValidatePath(inputPath, workingDir, workspaceRoot string) (string, error) {
	// Normalize and expand the input path
	expanded := expandPath(inputPath)

	// Resolve to absolute path
	var resolved string
	if filepath.IsAbs(expanded) {
		resolved = filepath.Clean(expanded)
	} else {
		resolved = filepath.Clean(filepath.Join(workingDir, expanded))
	}

	// Ensure workspace root is absolute and clean
	rootResolved := filepath.Clean(workspaceRoot)

	// Check if resolved path is within workspace root
	relative, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return "", fmt.Errorf("failed to compute relative path: %w", err)
	}

	// If relative path starts with ".." or is absolute, it escapes the sandbox
	if relative == "" {
		// Path is exactly the root - allowed
	} else if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		L_warn("sandbox: path escapes workspace", "path", inputPath, "resolved", resolved, "root", rootResolved)
		return "", fmt.Errorf("path escapes sandbox root (%s): %s", shortPath(rootResolved), inputPath)
	}

	// Check for symlinks in path (only for relative portion within workspace)
	if relative != "" && relative != "." {
		if err := assertNoSymlink(relative, rootResolved); err != nil {
			return "", err
		}
	}

	// Check against denied files list
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

// assertNoSymlink walks each component of the relative path and checks for symlinks.
// This prevents symlink attacks where a symlink points outside the sandbox.
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
				// Path doesn't exist yet - that's fine for write operations
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
// This is a convenience wrapper that combines validation and reading.
func ReadFile(inputPath, workingDir, workspaceRoot string) ([]byte, error) {
	resolved, err := ValidatePath(inputPath, workingDir, workspaceRoot)
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
// It preserves the original file's permissions if the file exists.
// If the file doesn't exist, it uses defaultPerm (or 0644 if 0).
func AtomicWriteFile(path string, data []byte, defaultPerm os.FileMode) error {
	// Determine permissions to use
	perm := defaultPerm
	if perm == 0 {
		perm = 0600
	}

	// Try to preserve existing file permissions
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
		L_trace("sandbox: preserving file permissions", "path", path, "perm", perm)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create temp file in same directory (required for atomic rename)
	tmpFile, err := os.CreateTemp(dir, ".goclaw-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on failure
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Write data to temp file
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Sync to disk
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set permissions on temp file
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}

	success = true
	return nil
}

// ValidateWritePath validates a path for write operations.
// In addition to standard path validation, it blocks writes to protected directories.
func ValidateWritePath(inputPath, workingDir, workspaceRoot string) (string, error) {
	resolved, err := ValidatePath(inputPath, workingDir, workspaceRoot)
	if err != nil {
		return "", err
	}

	rootResolved := filepath.Clean(workspaceRoot)
	relative, _ := filepath.Rel(rootResolved, resolved)

	for _, dir := range writeProtectedDirs {
		if strings.HasPrefix(relative, dir+string(filepath.Separator)) || relative == dir {
			L_warn("sandbox: write to protected directory blocked", "path", inputPath, "dir", dir)
			return "", fmt.Errorf("write denied: %s/ is read-only", dir)
		}
	}

	return resolved, nil
}

// WriteFileValidated validates the path for writes, then writes atomically.
// Combines path validation with atomic write for convenience.
func WriteFileValidated(inputPath, workingDir, workspaceRoot string, data []byte, defaultPerm os.FileMode) error {
	resolved, err := ValidateWritePath(inputPath, workingDir, workspaceRoot)
	if err != nil {
		return err
	}

	return AtomicWriteFile(resolved, data, defaultPerm)
}

// shortPath shortens a path by replacing home directory with ~
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
