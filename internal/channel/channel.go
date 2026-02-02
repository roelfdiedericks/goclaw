// Package channel provides the interface for messaging channels.
package channel

import (
	"context"

	"github.com/roelfdiedericks/goclaw/internal/user"
)

// Channel is the interface for messaging channels (TUI, Telegram, etc.)
type Channel interface {
	// Name returns the channel identifier (e.g., "tui", "telegram")
	Name() string

	// Start begins processing messages for this channel
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel
	Stop() error

	// Send sends a message through this channel
	Send(ctx context.Context, msg string) error

	// SendMirror sends a mirrored conversation from another channel
	SendMirror(ctx context.Context, source, userMsg, response string) error

	// HasUser returns true if this channel can reach the given user
	HasUser(u *user.User) bool
}
