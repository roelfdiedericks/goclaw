// Package events defines the event types for agent execution.
// These are in a separate package to avoid import cycles.
package events

import (
	"encoding/json"

	"github.com/roelfdiedericks/goclaw/internal/session"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// AgentEvent is the interface for all events emitted during an agent run
type AgentEvent interface {
	agentEvent() // marker method
}

// EventAgentStart is emitted when an agent run begins
type EventAgentStart struct {
	RunID      string `json:"runId"`
	Source     string `json:"source"`
	SessionKey string `json:"sessionKey"`
}

func (EventAgentStart) agentEvent() {}

// EventTextDelta is emitted for each text chunk from the LLM
type EventTextDelta struct {
	RunID string `json:"runId"`
	Delta string `json:"delta"`
}

func (EventTextDelta) agentEvent() {}

// EventToolStart is emitted when a tool execution begins
type EventToolStart struct {
	RunID    string          `json:"runId"`
	ToolName string          `json:"toolName"`
	ToolID   string          `json:"toolId"`
	Input    json.RawMessage `json:"input"`
}

func (EventToolStart) agentEvent() {}

// EventToolEnd is emitted when a tool execution completes
type EventToolEnd struct {
	RunID      string `json:"runId"`
	ToolName   string `json:"toolName"`
	ToolID     string `json:"toolId"`
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

func (EventToolEnd) agentEvent() {}

// EventAgentEnd is emitted when an agent run completes successfully
type EventAgentEnd struct {
	RunID     string `json:"runId"`
	FinalText string `json:"finalText"`
}

func (EventAgentEnd) agentEvent() {}

// EventAgentError is emitted when an agent run fails
type EventAgentError struct {
	RunID string `json:"runId"`
	Error string `json:"error"`
}

func (EventAgentError) agentEvent() {}

// EventThinking is emitted when thinking completes (batch mode - full content)
type EventThinking struct {
	RunID   string `json:"runId"`
	Content string `json:"content"`
}

func (EventThinking) agentEvent() {}

// EventThinkingDelta is emitted for each thinking content chunk during streaming
type EventThinkingDelta struct {
	RunID string `json:"runId"`
	Delta string `json:"delta"`
}

func (EventThinkingDelta) agentEvent() {}

// EventUserMessage is emitted when a user message is received (for supervision)
type EventUserMessage struct {
	Content    string `json:"content"`
	Source     string `json:"source"`               // "http", "telegram", "guidance", "ghostwrite"
	Supervisor string `json:"supervisor,omitempty"` // Supervisor username (for guidance/ghostwrite)
}

func (EventUserMessage) agentEvent() {}

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
	EnableThinking bool   // If true, enable extended thinking for models that support it
	ThinkingLevel  string // Thinking intensity: off/minimal/low/medium/high/xhigh (overrides EnableThinking)

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
