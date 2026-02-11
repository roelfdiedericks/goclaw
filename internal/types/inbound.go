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
	// Identity
	ID         string     // Unique message ID (auto-generated if empty)
	SessionKey string     // Session key: "primary", "user:alice", "cron:jobid", etc.
	User       *user.User // Who sent it (required for most operations)

	// Source identification
	Source string // "telegram", "http", "tui", "cron", "hass", "heartbeat", "supervision"

	// Content
	Text   string            // Message text
	Images []ImageAttachment // Attached images (multimodal)

	// Routing hints (channel-specific)
	ReplyTo string            // Channel-specific reply target (chat_id, conn_id)
	Meta    map[string]string // Channel-specific metadata (message_id, username, etc.)

	// Behavior flags
	Wake            bool   // Should this trigger agent run? (vs passive inject for context)
	SkipMirror      bool   // Don't mirror response to other channels
	SkipAddMessage  bool   // Don't add message to session (already added, e.g., supervision)
	FreshContext    bool   // Skip prior conversation history (isolated sessions)
	IsHeartbeat     bool   // Ephemeral run - don't persist to session
	SuppressPrefix  string // Suppress delivery if response contains this (e.g., "EVENT_OK")
	EnableThinking  bool   // Enable extended thinking mode

	// Supervision metadata
	Supervisor       *user.User // Who injected this (for audit)
	InterventionType string     // "guidance" or "ghostwrite" (supervision only)
}

// NewInboundMessage creates an InboundMessage with sensible defaults.
func NewInboundMessage(source string, u *user.User, text string) *InboundMessage {
	return &InboundMessage{
		Source:         source,
		User:           u,
		Text:           text,
		Wake:           true, // Default: trigger agent
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

// WithSuppression sets the suppression prefix (e.g., "EVENT_OK", "HEARTBEAT_OK").
func (m *InboundMessage) WithSuppression(prefix string) *InboundMessage {
	m.SuppressPrefix = prefix
	return m
}

// AsPassive marks this as a passive injection (no agent run).
func (m *InboundMessage) AsPassive() *InboundMessage {
	m.Wake = false
	return m
}

// AsIsolated marks this as an isolated session (fresh context).
func (m *InboundMessage) AsIsolated() *InboundMessage {
	m.FreshContext = true
	return m
}

// AsHeartbeat marks this as a heartbeat (ephemeral, not persisted).
func (m *InboundMessage) AsHeartbeat() *InboundMessage {
	m.IsHeartbeat = true
	return m
}

// ForSupervision sets supervision metadata.
func (m *InboundMessage) ForSupervision(supervisor *user.User, interventionType string) *InboundMessage {
	m.Supervisor = supervisor
	m.InterventionType = interventionType
	m.Source = "supervision"
	return m
}
