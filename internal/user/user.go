// Package user provides user identity, roles, and permission management.
package user

import "slices"

// Role represents a user's access level
type Role string

const (
	RoleOwner Role = "owner" // full access to everything
	RoleUser  Role = "user"  // chat + limited tools
	RoleGuest Role = "guest" // unauthenticated, minimal access
)

// User represents an authenticated user who can interact with the agent
type User struct {
	ID               string          // unique identifier (username from users.json key)
	Name             string          // display name
	Role             Role            // owner or user
	TelegramID       string          // Telegram user ID (for telegram auth)
	HTTPPasswordHash string          // Argon2id hash of HTTP password
	Permissions      map[string]bool // tool whitelist (nil = use role defaults)
}

// VerifyHTTPPassword checks if the password matches the stored hash
func (u *User) VerifyHTTPPassword(password string) bool {
	if u == nil || u.HTTPPasswordHash == "" {
		return false
	}
	return VerifyPassword(password, u.HTTPPasswordHash)
}

// HasHTTPAuth returns true if user has HTTP authentication configured
func (u *User) HasHTTPAuth() bool {
	return u != nil && u.HTTPPasswordHash != ""
}

// HasTelegramAuth returns true if user has Telegram authentication configured
func (u *User) HasTelegramAuth() bool {
	return u != nil && u.TelegramID != ""
}

// Default tool permissions by role
var defaultPermissions = map[Role][]string{
	RoleOwner: {"*"},                                          // everything
	RoleUser:  {"read", "web_search", "web_fetch", "transcript"}, // safe tools + own transcript
	RoleGuest: {"read"},                                       // minimal - read only
}

// CanUseTool checks if the user has permission to use a specific tool
func (u *User) CanUseTool(toolName string) bool {
	if u == nil {
		return false
	}
	
	// Owner can do anything
	if u.Role == RoleOwner {
		return true
	}
	
	// Check explicit permissions if set
	if u.Permissions != nil {
		return u.Permissions[toolName]
	}
	
	// Fall back to role defaults
	defaults := defaultPermissions[u.Role]
	return slices.Contains(defaults, toolName) || slices.Contains(defaults, "*")
}

// IsOwner returns true if the user has owner role
func (u *User) IsOwner() bool {
	return u != nil && u.Role == RoleOwner
}

// IsGuest returns true if the user has guest role
func (u *User) IsGuest() bool {
	return u != nil && u.Role == RoleGuest
}

// HasIdentity checks if the user has a specific identity
func (u *User) HasIdentity(provider, value string) bool {
	if u == nil {
		return false
	}
	switch provider {
	case "telegram":
		return u.TelegramID == value
	case "http":
		// HTTP auth uses username (user ID), not a separate identity value
		return u.ID == value && u.HTTPPasswordHash != ""
	}
	return false
}
