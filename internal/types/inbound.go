// Package types contains shared types used across multiple packages.
package types

import (
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// InboundMessage represents any message that triggers agent processing.
// This unifies user messages, HASS events, cron triggers, heartbeats, and supervision.
//
// All entry points (RunAgent, InvokeAgent, RunAgentForCron, InjectMessage, etc.)
// can be expressed in terms of InboundMessage, enabling future refactoring
// toward a message bus architecture.
type InboundMessage struct {
	// === Identity ===
	ID         string     // Unique message ID (auto-generated if empty)
	SessionKey string     // Session key: "primary", "user:alice", "cron:jobid", etc.
	User       *user.User // Who sent it (required for agent runs)

	// === Source ===
	Source string // "telegram", "http", "tui", "cron", "hass", "system", "guidance"

	// === Content ===
	Text   string            // Message text (empty = use existing session)
	Images []ImageAttachment // Attached images (multimodal)

	// === Routing ===
	ReplyTo string            // Channel-specific reply target (chat_id, conn_id)
	Meta    map[string]string // Channel-specific metadata (message_id, username, etc.)

	// === Agent Targeting ===
	RunAgent bool   // true = run agent, false = inject to context only
	AgentID  string // "main" (default), future: "subagent:xyz"

	// === Behavior Flags ===
	SkipMirror     bool // Don't mirror response to other channels
	FreshContext   bool // Skip prior conversation history (isolated sessions)
	Ephemeral      bool // No persistence, rollback after run (was: IsHeartbeat)
	EnableThinking bool // Enable extended thinking mode

	// === Suppression ===
	SuppressDeliveryOn string // If response contains this, suppress delivery (e.g., "EVENT_OK")

	// === Status Message ===
	StatusMessage string // Optional status to send before processing (caller decides)

	// === Supervision ===
	Supervisor       *user.User // Who injected this (for audit)
	InterventionType string     // "guidance" or "ghostwrite" (supervision only)
}

// NewInboundMessage creates an InboundMessage with sensible defaults.
func NewInboundMessage(source string, u *user.User, text string) *InboundMessage {
	return &InboundMessage{
		Source:         source,
		User:           u,
		Text:           text,
		RunAgent:       true,   // Default: trigger agent
		AgentID:        "main", // Default: main agent
		EnableThinking: u != nil && u.Thinking,
	}
}

// WithSessionKey sets the session key.
func (m *InboundMessage) WithSessionKey(key string) *InboundMessage {
	m.SessionKey = key
	return m
}

// WithImages attaches images to the message.
func (m *InboundMessage) WithImages(images []ImageAttachment) *InboundMessage {
	m.Images = images
	return m
}

// WithMeta sets channel-specific metadata.
func (m *InboundMessage) WithMeta(key, value string) *InboundMessage {
	if m.Meta == nil {
		m.Meta = make(map[string]string)
	}
	m.Meta[key] = value
	return m
}

// WithSuppressDeliveryOn sets the suppression match string.
// If the agent's response contains this string (case-insensitive), delivery is suppressed.
func (m *InboundMessage) WithSuppressDeliveryOn(match string) *InboundMessage {
	m.SuppressDeliveryOn = match
	return m
}

// WithStatusMessage sets a status message to send before processing.
func (m *InboundMessage) WithStatusMessage(msg string) *InboundMessage {
	m.StatusMessage = msg
	return m
}

// WithoutRunAgent marks this as a passive injection (no agent run).
func (m *InboundMessage) WithoutRunAgent() *InboundMessage {
	m.RunAgent = false
	return m
}

// AsIsolated marks this as an isolated session (fresh context).
func (m *InboundMessage) AsIsolated() *InboundMessage {
	m.FreshContext = true
	return m
}

// AsEphemeral marks this as ephemeral (not persisted to session history).
func (m *InboundMessage) AsEphemeral() *InboundMessage {
	m.Ephemeral = true
	return m
}

// ForSupervision sets supervision metadata.
func (m *InboundMessage) ForSupervision(supervisor *user.User, interventionType string) *InboundMessage {
	m.Supervisor = supervisor
	m.InterventionType = interventionType
	m.Source = "guidance"
	return m
}
