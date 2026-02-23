// Package paths provides centralized path resolution for GoClaw.
// This package has NO internal imports (only stdlib) to avoid import cycles.
// All functions return errors to allow callers to log appropriately.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaseDir returns the GoClaw base directory (~/.goclaw).
func BaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".goclaw"), nil
}

// DataPath returns a path within the GoClaw data directory (~/.goclaw/<subpath>).
func DataPath(subpath string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, subpath), nil
}

// ConfigPath returns the active goclaw.json path.
// Priority: ./goclaw.json (current dir) > ~/.goclaw/goclaw.json
// Returns ("", nil) if no config exists - this is a valid state, not an error.
func ConfigPath() (string, error) {
	// Check local first
	localPath := "goclaw.json"
	if _, err := os.Stat(localPath); err == nil {
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path: %w", err)
		}
		return absPath, nil
	}

	// Check global
	globalPath, err := DataPath("goclaw.json")
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath, nil
	}

	// No config found - valid state
	return "", nil
}

// DefaultConfigPath returns the default location for new configs (~/.goclaw/goclaw.json).
func DefaultConfigPath() (string, error) {
	return DataPath("goclaw.json")
}

// UsersPath returns the users.json path (alongside config).
// If configPath is empty, returns the default location.
func UsersPath(configPath string) (string, error) {
	if configPath == "" {
		return DataPath("users.json")
	}
	return filepath.Join(filepath.Dir(configPath), "users.json"), nil
}

// DefaultWorkspace returns the default workspace path (~/.goclaw/workspace).
func DefaultWorkspace() (string, error) {
	return DataPath("workspace")
}

// EnsureDir creates a directory if it doesn't exist.
// Uses 0750 permissions (owner: rwx, group: rx, other: none).
func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// EnsureParentDir creates the parent directory of a file path if it doesn't exist.
func EnsureParentDir(filePath string) error {
	return EnsureDir(filepath.Dir(filePath))
}

// ExpandTilde expands a path that starts with ~ to the user's home directory.
// Returns the path unchanged if it doesn't start with ~.
func ExpandTilde(path string) (string, error) {
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	if len(path) == 1 {
		return home, nil
	}
	return filepath.Join(home, path[1:]), nil
}

// OpenClawBaseDir returns the OpenClaw base directory (~/.openclaw).
func OpenClawBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".openclaw"), nil
}

// IsOpenClawWorkspace checks if a workspace path is inside the OpenClaw directory.
// Used to determine whether to use ~/.openclaw/ or ~/.goclaw/ for data paths.
func IsOpenClawWorkspace(workspacePath string) bool {
	openclawRoot, err := OpenClawBaseDir()
	if err != nil {
		return false
	}
	absWorkspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absWorkspace, openclawRoot)
}

// ContextualDataPath returns a data path based on workspace context.
// If workspaceDir is inside ~/.openclaw/, returns ~/.openclaw/<subpath>.
// Otherwise returns ~/.goclaw/<subpath>.
// This allows GoClaw to coexist with OpenClaw when running side-by-side.
func ContextualDataPath(subpath, workspaceDir string) (string, error) {
	if IsOpenClawWorkspace(workspaceDir) {
		base, err := OpenClawBaseDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, subpath), nil
	}
	return DataPath(subpath)
}
