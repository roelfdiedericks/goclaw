package auth

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ImplicitAuth provides authentication for trusted local access (TUI)
// It always returns the owner user without verification
type ImplicitAuth struct {
	users *user.Registry
}

// NewImplicitAuth creates an authenticator for local/trusted access
func NewImplicitAuth(users *user.Registry) *ImplicitAuth {
	return &ImplicitAuth{users: users}
}

// AuthType returns AuthImplicit
func (a *ImplicitAuth) AuthType() AuthType {
	return AuthImplicit
}

// Authenticate returns the owner user (TUI is trusted, owner-only)
func (a *ImplicitAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	owner := a.users.Owner()
	if owner == nil {
		return nil, ErrUserNotFound
	}
	return &AuthResult{User: owner}, nil
}
