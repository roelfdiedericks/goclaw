package auth

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// PlatformAuth provides authentication for platform-verified identities
// (e.g., Telegram, Discord where the platform has already verified the user)
type PlatformAuth struct {
	provider string // "telegram", "discord", etc.
}

// NewPlatformAuth creates an authenticator for platform-verified access
func NewPlatformAuth(provider string) *PlatformAuth {
	return &PlatformAuth{provider: provider}
}

// AuthType returns AuthPlatform
func (a *PlatformAuth) AuthType() AuthType {
	return AuthPlatform
}

// Authenticate trusts the platform-provided user ID
func (a *PlatformAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	if req.PlatformUserID == "" {
		return nil, ErrNoPlatformUserID
	}

	// Platform already verified the user - trust the ID
	return &AuthResult{
		Identity: user.Identity{
			Provider: a.provider,
			Value:    req.PlatformUserID,
		},
	}, nil
}
