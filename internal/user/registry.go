package user

import (
	"fmt"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/config"
)

// Registry maintains the set of known users and provides lookup by identity
type Registry struct {
	users    map[string]*User  // by user ID
	identity map[string]string // "telegram:123456" -> user ID
	ownerID  string            // cached owner user ID
	mu       sync.RWMutex
}

// NewRegistry creates a user registry from config
func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{
		users:    make(map[string]*User),
		identity: make(map[string]string),
	}

	for id, userCfg := range cfg.Users {
		user := &User{
			ID:          id,
			Name:        userCfg.Name,
			Role:        Role(userCfg.Role),
			Identities:  make([]Identity, 0, len(userCfg.Identities)),
			Credentials: make([]StoredCredential, 0, len(userCfg.Credentials)),
		}

		// Convert identities
		for _, idCfg := range userCfg.Identities {
			identity := Identity{
				Provider: idCfg.Provider,
				Value:    idCfg.ID,
			}
			user.Identities = append(user.Identities, identity)

			// Build identity lookup map
			key := fmt.Sprintf("%s:%s", idCfg.Provider, idCfg.ID)
			r.identity[key] = id
		}

		// Convert credentials
		for _, credCfg := range userCfg.Credentials {
			user.Credentials = append(user.Credentials, StoredCredential{
				Type:  credCfg.Type,
				Hash:  credCfg.Hash,
				Label: credCfg.Label,
			})
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
// Returns nil if no user is found with that identity
func (r *Registry) FromIdentity(provider, value string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", provider, value)
	userID, ok := r.identity[key]
	if !ok {
		return nil
	}
	return r.users[userID]
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
