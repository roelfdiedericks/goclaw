package gateway

import (
	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// MediaCallback is called when tool results contain media to send to the channel
type MediaCallback func(path, caption string) error

// AgentRequest contains all info needed to route and execute an agent run
type AgentRequest struct {
	User          *user.User                // authenticated user (nil = reject)
	Source        string                    // "tui", "telegram"
	ChatID        string                    // for telegram: chat ID; for TUI: empty
	IsGroup       bool                      // true if group chat (MVP: always false)
	UserMsg       string                    // the user's message
	Images        []session.ImageAttachment // image attachments (for multimodal)
	OnMediaToSend MediaCallback             // optional callback for sending media to channel

	// Cron-specific fields
	SessionID    string // Override session ID (e.g., "cron:<jobId>" for isolated jobs)
	FreshContext bool   // If true, skip prior conversation history (isolated cron jobs)

	// Heartbeat-specific fields
	IsHeartbeat bool // If true, run is ephemeral - don't persist to session

	// Supervision-specific fields
	SkipAddMessage bool // If true, don't add UserMsg to session (already added by supervision)

	// Thinking mode
	EnableThinking bool // If true, enable extended thinking for models that support it

	// Mirroring control
	SkipMirror bool // If true, don't mirror to other channels (caller handles delivery)
}

// HealthStatus provides gateway health information
type HealthStatus struct {
	Status       string `json:"status"` // "healthy", "degraded", "unhealthy"
	SessionCount int    `json:"sessionCount"`
	UserCount    int    `json:"userCount"`
	Uptime       int64  `json:"uptime"` // seconds
}
