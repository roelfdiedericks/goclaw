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
	Name             string `json:"name"`                         // Display name
	Role             string `json:"role"`                         // "owner" or "user"
	TelegramID       string `json:"telegram_id,omitempty"`        // Telegram user ID (numeric string)
	HTTPPasswordHash string `json:"http_password_hash,omitempty"` // Argon2id hash of HTTP password
	Thinking         *bool  `json:"thinking,omitempty"`           // Default /thinking toggle state (nil = role default)
	Sandbox          *bool  `json:"sandbox,omitempty"`            // Enable file sandboxing (nil = role default)
}

// applyDefaults sets role-based defaults for nil Thinking and Sandbox fields
func (e *UserEntry) applyDefaults() {
	isOwner := e.Role == "owner"

	if e.Thinking == nil {
		val := isOwner // true for owner, false for others
		e.Thinking = &val
	}
	if e.Sandbox == nil {
		val := !isOwner // false for owner, true for others
		e.Sandbox = &val
	}
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

// LoadUsers loads users from users.json
// Search order (highest priority first):
// 1. ./users.json (current directory - for development)
// 2. ~/.goclaw/users.json (primary location)
func LoadUsers() (UsersConfig, error) {
	home, _ := os.UserHomeDir()
	goclawDir := filepath.Join(home, ".goclaw")

	paths := []string{
		"users.json",                           // current directory
		filepath.Join(goclawDir, "users.json"), // ~/.goclaw/
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

	// Validate all users and apply defaults
	ownerCount := 0
	usersWithoutCredentials := 0
	for username, entry := range users {
		if err := ValidateUsername(username); err != nil {
			return nil, err
		}
		if entry.Role == "owner" {
			ownerCount++
		}
		// Allow any role string - validation against roles config happens in registry
		if entry.Role == "" {
			return nil, fmt.Errorf("user %q has no role defined", username)
		}
		// Warn about users without credentials (but don't fail - allows CLI setup flow)
		if entry.TelegramID == "" && entry.HTTPPasswordHash == "" {
			usersWithoutCredentials++
		}
		// Apply role-based defaults for thinking/sandbox
		entry.applyDefaults()
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
func GetUsersFilePath() string {
	home, _ := os.UserHomeDir()
	goclawDir := filepath.Join(home, ".goclaw")

	// Check existing files
	paths := []string{
		"users.json",                           // current directory
		filepath.Join(goclawDir, "users.json"), // ~/.goclaw/
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath
		}
	}

	// Default: ~/.goclaw/
	return filepath.Join(goclawDir, "users.json")
}

// GetUsersFilePathForConfig returns the users.json path alongside a given config path
// This ensures users.json stays in the same directory as goclaw.json
func GetUsersFilePathForConfig(configPath string) string {
	if configPath == "" {
		return GetUsersFilePath()
	}
	dir := filepath.Dir(configPath)
	return filepath.Join(dir, "users.json")
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
