package auth

import (
	"context"
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ChallengeAuth provides credential-based authentication
// Used for WebSocket, API, and other channels that require explicit verification
type ChallengeAuth struct {
	users *user.Registry
}

// NewChallengeAuth creates an authenticator that verifies credentials
func NewChallengeAuth(users *user.Registry) *ChallengeAuth {
	return &ChallengeAuth{users: users}
}

// AuthType returns AuthChallenge
func (a *ChallengeAuth) AuthType() AuthType {
	return AuthChallenge
}

// Authenticate verifies credentials against stored hashes
func (a *ChallengeAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	if req.Credentials == nil {
		return nil, ErrNoCredentials
	}

	// Find user with matching credential
	u, err := a.users.VerifyCredential(req.Credentials.Method, req.Credentials.Secret)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	return &AuthResult{
		Identity: user.Identity{
			Provider: "credential",
			Value:    u.ID,
		},
	}, nil
}
