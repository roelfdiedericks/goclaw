// Package user provides user identity, roles, and permission management.
package user

import "slices"

// Role represents a user's access level
type Role string

const (
	RoleOwner Role = "owner" // full access to everything
	RoleUser  Role = "user"  // chat + limited tools
)

// User represents an authenticated user who can interact with the agent
type User struct {
	ID          string            // unique identifier (config key)
	Name        string            // display name
	Role        Role              // owner or user
	Identities  []Identity        // ways to authenticate this user
	Credentials []StoredCredential // for challenge auth (passwords, API keys)
	Permissions map[string]bool   // tool whitelist (nil = use role defaults)
}

// Identity maps an external identity to this user
type Identity struct {
	Provider string // "telegram", "local", "apikey", etc.
	Value    string // telegram user ID, "owner" for local, etc.
}

// StoredCredential holds a hashed credential for challenge auth
type StoredCredential struct {
	Type  string // "apikey", "password"
	Hash  string // argon2/bcrypt hash (NEVER plaintext)
	Label string // "laptop-key", "phone-key" - for management
}

// Default tool permissions by role
var defaultPermissions = map[Role][]string{
	RoleOwner: {"*"}, // everything
	RoleUser:  {"read", "web_search", "web_fetch"}, // safe tools only
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

// HasIdentity checks if the user has a specific identity
func (u *User) HasIdentity(provider, value string) bool {
	for _, id := range u.Identities {
		if id.Provider == provider && id.Value == value {
			return true
		}
	}
	return false
}
