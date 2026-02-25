package user

import (
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/logging"
)

// Registry maintains the set of known users and provides lookup by identity
type Registry struct {
	users       map[string]*User  // by username (user ID)
	telegramID  map[string]string // telegram user ID -> username
	whatsappID  map[string]string // whatsapp JID -> username
	ownerID     string            // cached owner username
	rolesConfig RolesConfig       // role definitions from goclaw.json
	mu          sync.RWMutex
}

// NewRegistryFromUsers creates a user registry from UsersConfig
// The rolesConfig is used to validate user roles and resolve permissions
func NewRegistryFromUsers(users UsersConfig, rolesConfig RolesConfig) *Registry {
	r := &Registry{
		users:       make(map[string]*User),
		telegramID:  make(map[string]string),
		whatsappID:  make(map[string]string),
		rolesConfig: rolesConfig,
	}

	for username, entry := range users {
		// Validate that the user's role can be resolved
		_, err := ResolveRole(entry.Role, rolesConfig)
		if err != nil {
			logging.L_error("user: skipping user with unresolvable role",
				"username", username,
				"role", entry.Role,
				"error", err)
			continue
		}

		// Resolve thinking level from user config
		thinkingLevel := ""
		if entry.ThinkingLevel != nil {
			thinkingLevel = *entry.ThinkingLevel
		}

		user := &User{
			ID:               username,
			Name:             entry.Name,
			Role:             Role(entry.Role),
			TelegramID:       entry.TelegramID,
			WhatsAppID:       entry.WhatsAppID,
			HTTPPasswordHash: entry.HTTPPasswordHash,
			Thinking:         entry.Thinking != nil && *entry.Thinking,
			ThinkingLevel:    thinkingLevel,
			Sandbox:          entry.Sandbox == nil || *entry.Sandbox, // default true if nil
		}

		r.users[username] = user

		if !user.Sandbox {
			logging.L_warn("users: user running with sandbox disabled",
				"user", username, "role", entry.Role)
		}

		// Build identity lookup maps
		if entry.TelegramID != "" {
			r.telegramID[entry.TelegramID] = username
		}
		if entry.WhatsAppID != "" {
			r.whatsappID[entry.WhatsAppID] = username
		}

		// Track owner
		if user.Role == RoleOwner {
			r.ownerID = username
		}
	}

	return r
}

// GetRolesConfig returns the roles configuration
func (r *Registry) GetRolesConfig() RolesConfig {
	return r.rolesConfig
}

// ResolveUserRole resolves a user's role to its permissions
func (r *Registry) ResolveUserRole(u *User) (*ResolvedRole, error) {
	if u == nil {
		return nil, nil
	}
	return ResolveRole(string(u.Role), r.rolesConfig)
}

// FromIdentity looks up a user by their external identity
// Supported providers: "telegram", "whatsapp"
// Returns nil if no user is found with that identity
func (r *Registry) FromIdentity(provider, value string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch provider {
	case "telegram":
		if username, ok := r.telegramID[value]; ok {
			return r.users[username]
		}
	case "whatsapp":
		if username, ok := r.whatsappID[value]; ok {
			return r.users[username]
		}
	}

	return nil
}

// FromTelegramID looks up a user by their Telegram user ID
func (r *Registry) FromTelegramID(telegramID string) *User {
	return r.FromIdentity("telegram", telegramID)
}

// FromWhatsAppID looks up a user by their WhatsApp JID
func (r *Registry) FromWhatsAppID(whatsappID string) *User {
	return r.FromIdentity("whatsapp", whatsappID)
}

// Owner returns the owner user (first user with owner role)
// Returns nil if no owner is configured
func (r *Registry) Owner() *User {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.ownerID == "" {
		return nil
	}
	return r.users[r.ownerID]
}

// Get returns a user by their ID
// Returns nil if not found
func (r *Registry) Get(id string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.users[id]
}

// List returns all users
func (r *Registry) List() []*User {
	r.mu.RLock()
	defer r.mu.RUnlock()

	users := make([]*User, 0, len(r.users))
	for _, u := range r.users {
		users = append(users, u)
	}
	return users
}

// Count returns the number of registered users
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.users)
}
