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
type SandboxVolume struct {
	MountPoint string // Where it appears inside sandbox (e.g., /home/user/.config)
	Source     string // Actual backing directory (e.g., ~/.goclaw/sandbox/volumes/.config)
}

// Manager is the singleton sandbox manager.
// Holds all sandbox state: config, mode, volumes, protected dirs, paths.
type Manager struct {
	mu            sync.RWMutex
	config        Config
	workspaceRoot string
	mode          string
	dataDir       string
	homeDir       string
	volumes       []SandboxVolume
	protectedDirs map[string]string // relative -> absolute
	extraPaths    []string
}

var (
	instance *Manager
	once     sync.Once
)

// InitManager creates and initializes the singleton sandbox manager.
// Called once from main.go early in startup. Panics on re-init.
func InitManager(cfg Config, workspaceRoot string) *Manager {
	once.Do(func() {
		absRoot, err := filepath.Abs(workspaceRoot)
		if err != nil {
			L_error("sandbox: failed to resolve workspace root", "error", err)
			absRoot = workspaceRoot
		}

		m := &Manager{
			config:        cfg,
			workspaceRoot: absRoot,
			protectedDirs: make(map[string]string),
			extraPaths:    cfg.Bubblewrap.ExtraPaths,
		}

		// Register default protected dirs
		for _, dir := range defaultWriteProtectedDirs {
			if err := m.registerProtectedDirLocked(dir); err != nil {
				L_warn("sandbox: failed to register default protected dir", "dir", dir, "error", err)
			}
		}

		// Resolve mode
		m.mode = cfg.Bubblewrap.GetMode()

		// Resolve dataDir
		if cfg.Bubblewrap.DataDir != "" {
			m.dataDir = expandHomePath(cfg.Bubblewrap.DataDir)
		} else {
			resolved, err := paths.DataPath("sandbox")
			if err != nil {
				L_error("sandbox: failed to resolve default dataDir", "error", err)
			} else {
				m.dataDir = resolved
			}
		}

		if m.dataDir != "" {
			if err := os.MkdirAll(m.dataDir, 0750); err != nil {
				L_error("sandbox: failed to create dataDir", "path", m.dataDir, "error", err)
			}
		}

		// Set up mode-specific state
		switch m.mode {
		case ModeHome:
			if m.dataDir != "" {
				m.homeDir = filepath.Join(m.dataDir, "home")
				if err := os.MkdirAll(m.homeDir, 0750); err != nil {
					L_error("sandbox: failed to create home dir", "path", m.homeDir, "error", err)
				}
			}
		case ModeVolumes:
			vols := cfg.Bubblewrap.Volumes
			if len(vols) == 0 {
				vols = DefaultVolumes()
			}
			for _, mountPoint := range vols {
				expanded := expandHomePath(mountPoint)
				baseName := filepath.Base(expanded)
				source := filepath.Join(m.dataDir, "volumes", baseName)
				if err := os.MkdirAll(source, 0750); err != nil {
					L_warn("sandbox: failed to create volume dir", "volume", mountPoint, "error", err)
					continue
				}
				m.volumes = append(m.volumes, SandboxVolume{
					MountPoint: expanded,
					Source:     source,
				})
				L_debug("sandbox: registered volume", "mountPoint", expanded, "source", source)
			}
		case ModeEphemeral:
			// Nothing to create
		}

		L_info("sandbox: initialized",
			"mode", m.mode,
			"workspace", absRoot,
			"dataDir", m.dataDir,
			"volumes", len(m.volumes),
			"protectedDirs", len(m.protectedDirs))

		instance = m
	})
	return instance
}

// GetManager returns the singleton sandbox manager.
// Panics if not initialized.
func GetManager() *Manager {
	if instance == nil {
		panic("sandbox: manager not initialized - call InitManager first")
	}
	return instance
}

// --- Sandbox state API ---

// BuildSandboxPATH constructs the full PATH for use inside the bwrap sandbox.
// System paths first, then common user bin dirs, then user-configured extras.
func (m *Manager) BuildSandboxPATH(home string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

	for _, dir := range UserBinDirs() {
		path += ":" + filepath.Join(home, dir)
	}

	for _, p := range m.extraPaths {
		expanded := p
		if strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		}
		path += ":" + expanded
	}

	return path
}

// GetBinSearchDirs returns absolute paths to search for binaries outside the sandbox.
// Used by skills eligibility checker to find agent-installed binaries.
func (m *Manager) GetBinSearchDirs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dirs []string

	if m.homeDir != "" {
		for _, dir := range UserBinDirs() {
			dirs = append(dirs, filepath.Join(m.homeDir, dir))
		}
	}

	for _, vol := range m.volumes {
		for _, dir := range UserBinDirs() {
			dirs = append(dirs, filepath.Join(vol.Source, dir))
		}
	}

	return dirs
}

// GetProtectedDirs returns all registered write-protected directories as absolute paths.
func (m *Manager) GetProtectedDirs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.protectedDirs))
	for _, absPath := range m.protectedDirs {
		result = append(result, absPath)
	}
	return result
}

// GetVolumes returns all registered sandbox volumes.
func (m *Manager) GetVolumes() []SandboxVolume {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SandboxVolume, len(m.volumes))
	copy(result, m.volumes)
	return result
}

// GetHomeDir returns the sandbox home backing directory, or empty if not in home mode.
func (m *Manager) GetHomeDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.homeDir
}

// GetMode returns the current sandbox mode.
func (m *Manager) GetMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

// GetWorkspaceRoot returns the workspace root path.
func (m *Manager) GetWorkspaceRoot() string {
	return m.workspaceRoot
}

// IsRegisteredVolume checks if a home path is covered by sandbox isolation.
// In home mode, any ~/path is safe. In volumes mode, only listed volumes.
func (m *Manager) IsRegisteredVolume(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	expanded := expandHomePath(path)

	if m.mode == ModeHome {
		home, _ := os.UserHomeDir()
		if strings.HasPrefix(expanded, home+string(filepath.Separator)) {
			return true
		}
	}

	for _, vol := range m.volumes {
		if expanded == vol.MountPoint || strings.HasPrefix(expanded, vol.MountPoint+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// IsPathProtected checks if a relative path is within a write-protected directory.
func (m *Manager) IsPathProtected(relativePath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cleaned := filepath.Clean(relativePath)
	for protectedRel := range m.protectedDirs {
		if cleaned == protectedRel || strings.HasPrefix(cleaned, protectedRel+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// RegisterProtectedDir adds a directory to the write-protected list.
func (m *Manager) RegisterProtectedDir(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerProtectedDirLocked(dir)
}

func (m *Manager) registerProtectedDirLocked(dir string) error {
	var absPath string
	var relativePath string

	if filepath.IsAbs(dir) {
		absPath = filepath.Clean(dir)
		rel, err := filepath.Rel(m.workspaceRoot, absPath)
		if err != nil || filepath.IsAbs(rel) || hasParentPrefix(rel) {
			relativePath = absPath
		} else {
			relativePath = rel
		}
	} else {
		relativePath = filepath.Clean(dir)
		absPath = filepath.Join(m.workspaceRoot, relativePath)
	}

	if _, err := os.Stat(absPath); err == nil {
		if err := assertNoSymlinkPath(absPath); err != nil {
			return fmt.Errorf("cannot register path with symlinks: %w", err)
		}
	}

	m.protectedDirs[relativePath] = absPath
	L_debug("sandbox: registered protected dir", "path", relativePath, "abs", absPath)
	return nil
}

// --- Static helpers ---

// UserBinDirs returns common user binary directories relative to home.
func UserBinDirs() []string {
	return []string{
		".local/bin",
		".npm-global/bin",
		"go/bin",
		".cargo/bin",
		".bun/bin",
		"pip-tools/bin",
	}
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

// hasParentPrefix checks if a path starts with ".."
func hasParentPrefix(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

// assertNoSymlinkPath walks the path and checks for symlinks.
func assertNoSymlinkPath(absPath string) error {
	parts := strings.Split(absPath, string(filepath.Separator))
	current := ""

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
				return nil
			}
			return fmt.Errorf("failed to stat %s: %w", current, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink detected at %s", current)
		}
	}

	return nil
}
