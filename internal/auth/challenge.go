package auth

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ChallengeAuth provides HTTP Basic Auth authentication
// Used for HTTP API where we verify username/password
type ChallengeAuth struct {
	users *user.Registry
}

// NewChallengeAuth creates an authenticator that verifies HTTP Basic Auth credentials
func NewChallengeAuth(users *user.Registry) *ChallengeAuth {
	return &ChallengeAuth{users: users}
}

// AuthType returns AuthChallenge
func (a *ChallengeAuth) AuthType() AuthType {
	return AuthChallenge
}

// Authenticate verifies HTTP Basic Auth credentials against stored hashes
func (a *ChallengeAuth) Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error) {
	if req.Credentials == nil {
		return nil, ErrNoCredentials
	}

	// Look up user by username (= user ID in users.json)
	u := a.users.Get(req.Credentials.Username)
	if u == nil {
		return nil, ErrUserNotFound
	}

	// Verify password against stored hash
	if !u.VerifyHTTPPassword(req.Credentials.Password) {
		return nil, ErrAuthFailed
	}

	return &AuthResult{User: u}, nil
}
