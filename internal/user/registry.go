package user

import (
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/config"
)

// Registry maintains the set of known users and provides lookup by identity
type Registry struct {
	users      map[string]*User  // by username (user ID)
	telegramID map[string]string // telegram user ID -> username
	ownerID    string            // cached owner username
	mu         sync.RWMutex
}

// NewRegistryFromUsers creates a user registry from UsersConfig (new format)
func NewRegistryFromUsers(users config.UsersConfig) *Registry {
	r := &Registry{
		users:      make(map[string]*User),
		telegramID: make(map[string]string),
	}

	for username, entry := range users {
		user := &User{
			ID:               username,
			Name:             entry.Name,
			Role:             Role(entry.Role),
			TelegramID:       entry.TelegramID,
			HTTPPasswordHash: entry.HTTPPasswordHash,
		}

		r.users[username] = user

		// Build telegram lookup map
		if entry.TelegramID != "" {
			r.telegramID[entry.TelegramID] = username
		}

		// Track owner
		if user.Role == RoleOwner {
			r.ownerID = username
		}
	}

	return r
}

// NewRegistry creates a user registry from legacy config (deprecated, for compatibility)
func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{
		users:      make(map[string]*User),
		telegramID: make(map[string]string),
	}

	for id, userCfg := range cfg.Users {
		user := &User{
			ID:   id,
			Name: userCfg.Name,
			Role: Role(userCfg.Role),
		}

		// Convert identities to find telegram ID
		for _, idCfg := range userCfg.Identities {
			if idCfg.Provider == "telegram" {
				user.TelegramID = idCfg.ID
				r.telegramID[idCfg.ID] = id
			}
		}

		// Convert credentials to find HTTP password
		for _, credCfg := range userCfg.Credentials {
			if credCfg.Type == "password" {
				user.HTTPPasswordHash = credCfg.Hash
				break
			}
		}

		// Convert permissions to map
		if len(userCfg.Permissions) > 0 {
			user.Permissions = make(map[string]bool)
			for _, perm := range userCfg.Permissions {
				user.Permissions[perm] = true
			}
		}

		r.users[id] = user

		// Track owner
		if user.Role == RoleOwner {
			r.ownerID = id
		}
	}

	return r
}

// FromIdentity looks up a user by their external identity
// Supported providers: "telegram"
// Returns nil if no user is found with that identity
func (r *Registry) FromIdentity(provider, value string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch provider {
	case "telegram":
		if username, ok := r.telegramID[value]; ok {
			return r.users[username]
		}
	}

	return nil
}

// FromTelegramID looks up a user by their Telegram user ID
func (r *Registry) FromTelegramID(telegramID string) *User {
	return r.FromIdentity("telegram", telegramID)
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
