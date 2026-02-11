// Package user provides user identity, roles, and permission management.
package user

import (
	"fmt"
	"slices"

	"github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Role represents a user's access level
type Role string

const (
	RoleOwner Role = "owner" // full access to everything
	RoleUser  Role = "user"  // chat + limited tools
	RoleGuest Role = "guest" // unauthenticated, minimal access
)

// ResolvedRole contains the resolved permissions for a user's role
type ResolvedRole struct {
	Name             string   // Role name (e.g., "owner", "user", "guest")
	Tools            []string // Allowed tools (nil = all tools via "*")
	AllTools         bool     // True if all tools allowed ("*")
	Skills           []string // Allowed skills (nil = all skills via "*")
	AllSkills        bool     // True if all skills allowed ("*")
	Memory           string   // "full" or "none"
	Transcripts      string   // "all", "own", or "none"
	Commands         bool     // Whether slash commands are enabled
	SystemPrompt     string   // Inline system prompt text
	SystemPromptFile string   // Path to system prompt file
}

// BuiltinOwnerRole is the default role config for owner when not explicitly configured
var BuiltinOwnerRole = config.RoleConfig{
	Tools:       "*",
	Skills:      "*",
	Memory:      "full",
	Transcripts: "all",
	Commands:    true,
}

// ResolveRole resolves a role name to its permissions using the config.
// Returns error if role cannot be resolved (non-owner role not defined).
func ResolveRole(roleName string, rolesConfig config.RolesConfig) (*ResolvedRole, error) {
	// Check if role is explicitly defined in config
	if roleConfig, ok := rolesConfig[roleName]; ok {
		return resolveFromConfig(roleName, &roleConfig), nil
	}

	// Special case: owner role gets built-in defaults if not defined
	if roleName == "owner" {
		// Warn if roles section exists but owner not defined
		if len(rolesConfig) > 0 {
			logging.L_warn("user: owner role not defined in roles config, using built-in defaults")
		}
		return resolveFromConfig("owner", &BuiltinOwnerRole), nil
	}

	// Non-owner role not found - this is an error
	if len(rolesConfig) == 0 {
		return nil, fmt.Errorf("role '%s' cannot be resolved: no 'roles' section in goclaw.json. Add a roles section to define permissions for non-owner users", roleName)
	}
	return nil, fmt.Errorf("role '%s' not defined in goclaw.json roles section. Define '%s' role or change user's role to a defined one", roleName, roleName)
}

// resolveFromConfig converts a RoleConfig to a ResolvedRole
func resolveFromConfig(name string, cfg *config.RoleConfig) *ResolvedRole {
	resolved := &ResolvedRole{
		Name:             name,
		Memory:           cfg.Memory,
		Transcripts:      cfg.Transcripts,
		Commands:         cfg.Commands,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
	}

	// Resolve tools
	if tools, allTools := cfg.GetToolsList(); allTools {
		resolved.AllTools = true
		resolved.Tools = nil
	} else {
		resolved.AllTools = false
		resolved.Tools = tools
	}

	// Resolve skills
	if skills, allSkills := cfg.GetSkillsList(); allSkills {
		resolved.AllSkills = true
		resolved.Skills = nil
	} else {
		resolved.AllSkills = false
		resolved.Skills = skills
	}

	// Apply defaults for empty values
	if resolved.Memory == "" {
		resolved.Memory = "none"
	}
	if resolved.Transcripts == "" {
		resolved.Transcripts = "none"
	}

	return resolved
}

// CanUseTool checks if the resolved role allows a specific tool
func (r *ResolvedRole) CanUseTool(toolName string) bool {
	if r.AllTools {
		return true
	}
	return slices.Contains(r.Tools, toolName)
}

// CanUseSkill checks if the resolved role allows a specific skill
func (r *ResolvedRole) CanUseSkill(skillName string) bool {
	if r.AllSkills {
		return true
	}
	return slices.Contains(r.Skills, skillName)
}

// CanUseCommands returns whether slash commands are enabled
func (r *ResolvedRole) CanUseCommands() bool {
	return r.Commands
}

// HasMemoryAccess returns whether the role has memory access
func (r *ResolvedRole) HasMemoryAccess() bool {
	return r.Memory == "full"
}

// GetTranscriptScope returns the transcript access scope
func (r *ResolvedRole) GetTranscriptScope() string {
	return r.Transcripts
}

// User represents an authenticated user who can interact with the agent
type User struct {
	ID               string          // unique identifier (username from users.json key)
	Name             string          // display name
	Role             Role            // owner or user
	TelegramID       string          // Telegram user ID (for telegram auth)
	HTTPPasswordHash string          // Argon2id hash of HTTP password
	Permissions      map[string]bool // tool whitelist (nil = use role defaults)
	Thinking         bool            // default /thinking toggle state
	Sandbox          bool            // enable file sandboxing
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
