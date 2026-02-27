package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// SandboxVolume represents an isolated mount point inside the bwrap sandbox.
// The real host directory is replaced with a clean, persistent backing directory.
type SandboxVolume struct {
	MountPoint string // Where it appears inside sandbox (e.g., /home/user/.config)
	Source     string // Actual backing directory (e.g., ~/.goclaw/sandbox/config)
}

var (
	protectedDirsMu sync.RWMutex
	protectedDirs   = make(map[string]string) // relative -> absolute mapping
	registryRoot    string                    // workspace root for relative path resolution

	volumesMu sync.RWMutex
	volumes   []SandboxVolume
)

// InitRegistry initializes the sandbox registry with the workspace root.
// Must be called before any RegisterProtectedDir calls.
func InitRegistry(workspaceRoot string) error {
	protectedDirsMu.Lock()
	defer protectedDirsMu.Unlock()

	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return fmt.Errorf("failed to resolve workspace root: %w", err)
	}

	registryRoot = absRoot
	protectedDirs = make(map[string]string)

	volumesMu.Lock()
	volumes = nil
	volumesMu.Unlock()

	L_debug("sandbox: registry initialized", "root", absRoot)
	return nil
}

// RegisterProtectedDir adds a directory to the write-protected list.
// Relative paths are resolved against the workspace root.
// Absolute paths are used as-is.
// Validates that the path contains no symlinks.
func RegisterProtectedDir(dir string) error {
	protectedDirsMu.Lock()
	defer protectedDirsMu.Unlock()

	if registryRoot == "" {
		return fmt.Errorf("sandbox registry not initialized")
	}

	var absPath string
	var relativePath string

	if filepath.IsAbs(dir) {
		absPath = filepath.Clean(dir)
		// For absolute paths, try to compute relative path for display
		rel, err := filepath.Rel(registryRoot, absPath)
		if err != nil || filepath.IsAbs(rel) || hasParentPrefix(rel) {
			relativePath = absPath // Use absolute as key for external paths
		} else {
			relativePath = rel
		}
	} else {
		relativePath = filepath.Clean(dir)
		absPath = filepath.Join(registryRoot, relativePath)
	}

	// Validate no symlinks in path (if path exists)
	if _, err := os.Stat(absPath); err == nil {
		if err := assertNoSymlinkRegistry(absPath); err != nil {
			return fmt.Errorf("cannot register path with symlinks: %w", err)
		}
	}

	// Store the mapping
	protectedDirs[relativePath] = absPath

	L_debug("sandbox: registered protected dir", "path", relativePath, "abs", absPath)
	return nil
}

// GetProtectedDirs returns all registered protected directories as absolute paths.
func GetProtectedDirs() []string {
	protectedDirsMu.RLock()
	defer protectedDirsMu.RUnlock()

	result := make([]string, 0, len(protectedDirs))
	for _, absPath := range protectedDirs {
		result = append(result, absPath)
	}
	return result
}

// GetProtectedDirsRelative returns protected directories as relative paths (for workspace-relative checks).
func GetProtectedDirsRelative() []string {
	protectedDirsMu.RLock()
	defer protectedDirsMu.RUnlock()

	result := make([]string, 0, len(protectedDirs))
	for relPath := range protectedDirs {
		result = append(result, relPath)
	}
	return result
}

// IsPathProtected checks if a path is within a protected directory.
// The path should be relative to the workspace root.
func IsPathProtected(relativePath string) bool {
	protectedDirsMu.RLock()
	defer protectedDirsMu.RUnlock()

	cleaned := filepath.Clean(relativePath)
	for protectedRel := range protectedDirs {
		if cleaned == protectedRel || hasPrefix(cleaned, protectedRel+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// RegisterVolume registers a sandbox volume mount point.
// The mount point (e.g., "~/.config") is expanded and a backing directory
// is created under ~/.goclaw/sandbox/<dirname>/.
func RegisterVolume(mountPoint string) error {
	volumesMu.Lock()
	defer volumesMu.Unlock()

	// Expand ~ to absolute path
	expanded := expandHomePath(mountPoint)

	// Derive backing directory name from the dot-dir (e.g., .config -> config)
	baseName := filepath.Base(expanded)
	if strings.HasPrefix(baseName, ".") {
		baseName = baseName[1:]
	}

	// Create backing directory under ~/.goclaw/sandbox/
	sandboxBase, err := paths.DataPath("sandbox")
	if err != nil {
		return fmt.Errorf("failed to resolve sandbox base: %w", err)
	}
	source := filepath.Join(sandboxBase, baseName)
	if err := os.MkdirAll(source, 0750); err != nil {
		return fmt.Errorf("failed to create sandbox volume %s: %w", source, err)
	}

	volumes = append(volumes, SandboxVolume{
		MountPoint: expanded,
		Source:     source,
	})

	L_debug("sandbox: registered volume", "mountPoint", expanded, "source", source)
	return nil
}

// GetVolumes returns all registered sandbox volumes.
func GetVolumes() []SandboxVolume {
	volumesMu.RLock()
	defer volumesMu.RUnlock()

	result := make([]SandboxVolume, len(volumes))
	copy(result, volumes)
	return result
}

// IsRegisteredVolume checks if a home dot-directory path is a registered volume.
// Accepts paths like "~/.config", "~/.config/", "~/.config/moltbook/credentials.json"
// and checks if the base dot-directory (e.g., ~/.config) is a registered volume.
func IsRegisteredVolume(path string) bool {
	volumesMu.RLock()
	defer volumesMu.RUnlock()

	expanded := expandHomePath(path)
	for _, vol := range volumes {
		if expanded == vol.MountPoint || strings.HasPrefix(expanded, vol.MountPoint+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// expandHomePath expands ~ prefix to the user's home directory.
func expandHomePath(p string) string {
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// ClearRegistry removes all registered paths and volumes (used for testing or reconfiguration).
func ClearRegistry() {
	protectedDirsMu.Lock()
	protectedDirs = make(map[string]string)
	protectedDirsMu.Unlock()

	volumesMu.Lock()
	volumes = nil
	volumesMu.Unlock()
}

// hasParentPrefix checks if a path starts with ".."
func hasParentPrefix(path string) bool {
	return path == ".." || hasPrefix(path, ".."+string(filepath.Separator))
}

// hasPrefix is a simple string prefix check
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// assertNoSymlinkRegistry walks the path and checks for symlinks.
func assertNoSymlinkRegistry(absPath string) error {
	current := ""
	parts := splitPath(absPath)

	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" && filepath.IsAbs(absPath) {
			current = string(filepath.Separator) + part
		} else {
			current = filepath.Join(current, part)
		}

		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // Path doesn't exist yet - ok
			}
			return fmt.Errorf("failed to stat %s: %w", current, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink detected at %s", current)
		}
	}

	return nil
}

// splitPath splits a path into components
func splitPath(path string) []string {
	return filepath.SplitList(path)
}
