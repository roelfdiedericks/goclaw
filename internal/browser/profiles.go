package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// ProfileInfo contains information about a browser profile
type ProfileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`     // Total size in bytes
	LastUsed time.Time `json:"lastUsed"` // Last modification time
}

// ProfileManager handles browser profile operations
type ProfileManager struct {
	profilesDir string
}

// NewProfileManager creates a new profile manager
func NewProfileManager(profilesDir string) *ProfileManager {
	return &ProfileManager{
		profilesDir: profilesDir,
	}
}

// EnsureProfile ensures a profile directory exists
func (m *ProfileManager) EnsureProfile(name string) (string, error) {
	if name == "" {
		name = "default"
	}

	profileDir := filepath.Join(m.profilesDir, name)

	if err := os.MkdirAll(profileDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create profile directory: %w", err)
	}

	L_debug("browser: ensured profile", "name", name, "path", profileDir)
	return profileDir, nil
}

// GetProfileDir returns the path to a profile directory (does not create it)
func (m *ProfileManager) GetProfileDir(name string) string {
	if name == "" {
		name = "default"
	}
	return filepath.Join(m.profilesDir, name)
}

// ProfileExists checks if a profile exists
func (m *ProfileManager) ProfileExists(name string) bool {
	profileDir := m.GetProfileDir(name)
	info, err := os.Stat(profileDir)
	return err == nil && info.IsDir()
}

// ListProfiles returns information about all profiles
func (m *ProfileManager) ListProfiles() ([]ProfileInfo, error) {
	entries, err := os.ReadDir(m.profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ProfileInfo{}, nil
		}
		return nil, fmt.Errorf("failed to read profiles directory: %w", err)
	}

	var profiles []ProfileInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		profileDir := filepath.Join(m.profilesDir, entry.Name())
		info, err := m.getProfileInfo(entry.Name(), profileDir)
		if err != nil {
			L_warn("browser: failed to get profile info", "name", entry.Name(), "error", err)
			continue
		}

		profiles = append(profiles, info)
	}

	return profiles, nil
}

// getProfileInfo calculates profile information
func (m *ProfileManager) getProfileInfo(name, path string) (ProfileInfo, error) {
	info := ProfileInfo{
		Name: name,
		Path: path,
	}

	// Calculate total size and find most recent modification
	err := filepath.Walk(path, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !fileInfo.IsDir() {
			info.Size += fileInfo.Size()
		}

		if fileInfo.ModTime().After(info.LastUsed) {
			info.LastUsed = fileInfo.ModTime()
		}

		return nil
	})

	if err != nil {
		return info, err
	}

	return info, nil
}

// ClearProfile removes all data from a profile (cookies, cache, etc.)
func (m *ProfileManager) ClearProfile(name string) error {
	if name == "" {
		name = "default"
	}

	profileDir := m.GetProfileDir(name)

	if !m.ProfileExists(name) {
		return fmt.Errorf("profile does not exist: %s", name)
	}

	// Remove all contents but keep the directory
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return fmt.Errorf("failed to read profile directory: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(profileDir, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			L_warn("browser: failed to remove profile entry", "path", entryPath, "error", err)
		}
	}

	L_info("browser: cleared profile", "name", name)
	return nil
}

// DeleteProfile completely removes a profile
func (m *ProfileManager) DeleteProfile(name string) error {
	if name == "" {
		return fmt.Errorf("cannot delete default profile")
	}

	if name == "default" {
		return fmt.Errorf("cannot delete default profile")
	}

	profileDir := m.GetProfileDir(name)

	if !m.ProfileExists(name) {
		return fmt.Errorf("profile does not exist: %s", name)
	}

	if err := os.RemoveAll(profileDir); err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	L_info("browser: deleted profile", "name", name)
	return nil
}

// GetProfilesDir returns the profiles directory
func (m *ProfileManager) GetProfilesDir() string {
	return m.profilesDir
}

// FormatSize returns a human-readable size string
func FormatSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
