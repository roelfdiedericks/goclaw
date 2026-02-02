package auth

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ImplicitAuth provides authentication for trusted local access (TUI, localhost)
// It always returns the owner identity without verification
type ImplicitAuth struct {
	ownerID string
}

// NewImplicitAuth creates an authenticator for local/trusted access
func NewImplicitAuth(ownerID string) *ImplicitAuth {
	return &ImplicitAuth{ownerID: ownerID}
}

// AuthType returns AuthImplicit
func (a *ImplicitAuth) AuthType() AuthType {
	return AuthImplicit
}

// Authenticate returns the owner identity (localhost is trusted)
func (a *ImplicitAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	return &AuthResult{
		Identity: user.Identity{
			Provider: "local",
			Value:    a.ownerID,
		},
	}, nil
}
