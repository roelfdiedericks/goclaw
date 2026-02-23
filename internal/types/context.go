package types

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// SessionElevator is the interface for elevating user roles in a session.
// Implemented by session.Session.
type SessionElevator interface {
	ElevateUser(name, username, role, id string)
}

// SessionContext provides current session information for tools.
type SessionContext struct {
	Channel         string          // Current channel name (e.g., "telegram", "tui")
	ChatID          string          // Current chat ID
	OwnerChatID     string          // Owner's telegram chat ID (fallback for cron/heartbeat)
	User            *user.User      // Current user (for permission checks in tools)
	TranscriptScope string          // Transcript access scope: "all", "own", or "none"
	Session         SessionElevator // Session for role elevation (user_auth tool)
}

// sessionContextKey is used to store SessionContext in context.Context
type sessionContextKey struct{}

// WithSessionContext adds session context to a context.Context
func WithSessionContext(ctx context.Context, sc *SessionContext) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, sc)
}

// GetSessionContext extracts session context from context.Context
func GetSessionContext(ctx context.Context) *SessionContext {
	if sc, ok := ctx.Value(sessionContextKey{}).(*SessionContext); ok {
		return sc
	}
	return nil
}
