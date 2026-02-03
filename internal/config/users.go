// Package config provides configuration loading for GoClaw.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// UsersConfig is the root of users.json
// Map key is the username (also used for HTTP auth and session keys)
type UsersConfig map[string]*UserEntry

// UserEntry represents a single user in users.json
// The map key (username) is used for HTTP auth and non-owner session keys
type UserEntry struct {
	Name             string `json:"name"`                        // Display name
	Role             string `json:"role"`                        // "owner" or "user"
	TelegramID       string `json:"telegram_id,omitempty"`       // Telegram user ID (numeric string)
	HTTPPasswordHash string `json:"http_password_hash,omitempty"` // Argon2id hash of HTTP password
}

// Username validation: lowercase alphanumeric + underscore, 1-32 chars, starts with letter
var usernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidateUsername checks if a username is valid
func ValidateUsername(username string) error {
	if !usernameRegex.MatchString(username) {
		return fmt.Errorf("invalid username %q: must be 1-32 chars, lowercase alphanumeric + underscore, start with letter", username)
	}
	return nil
}

// LoadUsers loads users from users.json with same priority as goclaw.json:
// 1. ./users.json (current directory - highest priority)
// 2. ~/.openclaw/workspace/goclaw/users.json (workspace)
// 3. ~/.openclaw/users.json (global fallback)
func LoadUsers() (UsersConfig, error) {
	home, _ := os.UserHomeDir()
	openclawDir := filepath.Join(home, ".openclaw")
	workspaceGoclaw := filepath.Join(openclawDir, "workspace", "goclaw")

	// Priority order (highest first) - mirrors goclaw.json search
	paths := []string{
		"users.json",                              // current directory
		filepath.Join(workspaceGoclaw, "users.json"), // ~/.openclaw/workspace/goclaw/
		filepath.Join(openclawDir, "users.json"),  // ~/.openclaw/
	}

	var users UsersConfig
	var loadedFrom string

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			logging.L_warn("users: failed to read file", "path", path, "error", err)
			continue
		}

		absPath, _ := filepath.Abs(path)
		logging.L_debug("users: loading from file", "path", absPath, "size", len(data))

		if err := json.Unmarshal(data, &users); err != nil {
			return nil, fmt.Errorf("failed to parse users.json at %s: %w", absPath, err)
		}

		loadedFrom = absPath
		break // Use first found file
	}

	if users == nil {
		users = make(UsersConfig)
		logging.L_warn("users: no users.json found, starting with empty user list")
	} else {
		logging.L_info("users: loaded", "path", loadedFrom, "count", len(users))
	}

	// Validate all users
	ownerCount := 0
	usersWithoutCredentials := 0
	for username, entry := range users {
		if err := ValidateUsername(username); err != nil {
			return nil, err
		}
		if entry.Role == "owner" {
			ownerCount++
		}
		if entry.Role != "owner" && entry.Role != "user" {
			return nil, fmt.Errorf("invalid role %q for user %q: must be 'owner' or 'user'", entry.Role, username)
		}
		// Warn about users without credentials (but don't fail - allows CLI setup flow)
		if entry.TelegramID == "" && entry.HTTPPasswordHash == "" {
			usersWithoutCredentials++
		}
	}

	if usersWithoutCredentials > 0 {
		logging.L_warn("users: some users have no credentials configured", "count", usersWithoutCredentials)
	}
	if ownerCount > 1 {
		logging.L_warn("users: multiple owners configured", "count", ownerCount)
	}

	return users, nil
}

// SaveUsers saves users to the specified path
func SaveUsers(users UsersConfig, path string) error {
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal users: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write users.json: %w", err)
	}

	logging.L_info("users: saved", "path", path, "count", len(users))
	return nil
}

// GetUsersFilePath returns the path where users.json should be saved
// Checks same locations as LoadUsers, returns first existing or workspace default
func GetUsersFilePath() string {
	home, _ := os.UserHomeDir()
	openclawDir := filepath.Join(home, ".openclaw")
	workspaceGoclaw := filepath.Join(openclawDir, "workspace", "goclaw")

	// Check existing files in priority order
	paths := []string{
		"users.json",                              // current directory
		filepath.Join(workspaceGoclaw, "users.json"), // workspace
		filepath.Join(openclawDir, "users.json"),  // global
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath
		}
	}

	// Default to workspace location (alongside goclaw.json)
	return filepath.Join(workspaceGoclaw, "users.json")
}

// HasHTTPUsers returns true if any user has HTTP credentials configured
func (u UsersConfig) HasHTTPUsers() bool {
	for _, entry := range u {
		if entry.HTTPPasswordHash != "" {
			return true
		}
	}
	return false
}

// GetOwner returns the username of the owner, or empty if none
func (u UsersConfig) GetOwner() string {
	for username, entry := range u {
		if entry.Role == "owner" {
			return username
		}
	}
	return ""
}
