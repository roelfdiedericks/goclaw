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
)

// AuthType identifies the authentication mechanism used by a channel
type AuthType string

const (
	AuthImplicit  AuthType = "implicit"  // TUI, localhost - trusted by access
	AuthPlatform  AuthType = "platform"  // Telegram - platform verified the user
	AuthChallenge AuthType = "challenge" // WebSocket, API - we verify credentials
)

// Authenticator verifies user identity for a channel
type Authenticator interface {
	// AuthType returns the type of authentication this authenticator uses
	AuthType() AuthType

	// Authenticate verifies credentials and returns the user's identity
	Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error)
}

// AuthRequest contains the information needed to authenticate
type AuthRequest struct {
	// For platform auth (Telegram user ID, etc.)
	PlatformUserID string

	// For challenge auth (WebSocket, API)
	Credentials *Credentials
}

// Credentials for challenge-based authentication
type Credentials struct {
	Method   string // "apikey", "password"
	Username string // for password auth
	Secret   string // the password or API key
}

// AuthResult contains the verified identity
type AuthResult struct {
	Identity user.Identity
}
