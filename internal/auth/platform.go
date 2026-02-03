package auth

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// PlatformAuth provides authentication for platform-verified identities
// (e.g., Telegram where the platform has already verified the user)
type PlatformAuth struct {
	provider string         // "telegram", etc.
	users    *user.Registry // for looking up users by platform ID
}

// NewPlatformAuth creates an authenticator for platform-verified access
func NewPlatformAuth(provider string, users *user.Registry) *PlatformAuth {
	return &PlatformAuth{provider: provider, users: users}
}

// AuthType returns AuthPlatform
func (a *PlatformAuth) AuthType() AuthType {
	return AuthPlatform
}

// Authenticate looks up the user by platform ID
func (a *PlatformAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	if req.PlatformUserID == "" {
		return nil, ErrNoPlatformUserID
	}

	// Look up user by platform identity
	u := a.users.FromIdentity(a.provider, req.PlatformUserID)
	if u == nil {
		return nil, ErrUserNotFound
	}

	return &AuthResult{User: u}, nil
}
