// Package auth provides channel authentication mechanisms.
package auth

import (
	"context"
	"errors"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

var (
	ErrNoCredentials    = errors.New("credentials required")
	ErrAuthFailed       = errors.New("authentication failed")
	ErrNoPlatformUserID = errors.New("no platform user ID provided")
	ErrUserNotFound     = errors.New("user not found")
)

// AuthType identifies the authentication mechanism used by a channel
type AuthType string

const (
	AuthImplicit  AuthType = "implicit"  // TUI, localhost - trusted by access
	AuthPlatform  AuthType = "platform"  // Telegram - platform verified the user
	AuthChallenge AuthType = "challenge" // HTTP Basic Auth - we verify credentials
)

// Authenticator verifies user identity for a channel
type Authenticator interface {
	// AuthType returns the type of authentication this authenticator uses
	AuthType() AuthType

	// Authenticate verifies credentials and returns the authenticated user
	Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error)
}

// AuthRequest contains the information needed to authenticate
type AuthRequest struct {
	// For platform auth (Telegram user ID, etc.)
	PlatformUserID string

	// For challenge auth (HTTP Basic Auth)
	Credentials *Credentials
}

// Credentials for challenge-based authentication (HTTP Basic Auth)
type Credentials struct {
	Username string // HTTP username (= user ID in users.json)
	Password string // HTTP password (verified against argon2id hash)
}

// AuthResult contains the authenticated user
type AuthResult struct {
	User *user.User
}
