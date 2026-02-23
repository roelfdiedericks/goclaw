// Package types contains shared types used across multiple packages.
// This helps avoid import cycles between packages like llm and session.
package types

import (
	"encoding/json"
	"time"
)

// Message represents a single message in a conversation.
// Used by both session management and LLM providers.
type Message struct {
	ID            string          `json:"id"`
	Role          string          `json:"role"` // "user", "assistant", "tool_use", "tool_result"
	Content       string          `json:"content"`
	ContentBlocks []ContentBlock  `json:"contentBlocks,omitempty"` // Structured content (images, audio, etc.)
	Source        string          `json:"source"`                  // "tui", "telegram", etc.
	Timestamp     time.Time       `json:"timestamp"`
	ToolUseID     string          `json:"toolUseId,omitempty"` // for tool_use and tool_result
	ToolName      string          `json:"toolName,omitempty"`  // for tool_use
	ToolInput     json.RawMessage `json:"toolInput,omitempty"` // for tool_use
	Thinking      string          `json:"thinking,omitempty"`  // Reasoning/thinking content (Kimi, Deepseek, etc.)

	// Supervision metadata (for guidance/ghostwriting interventions)
	Supervisor       string `json:"supervisor,omitempty"`       // Username/ID of supervisor who intervened
	InterventionType string `json:"interventionType,omitempty"` // "guidance" or "ghostwrite"
}

// HasImages returns true if the message contains any image content blocks
func (m *Message) HasImages() bool {
	for _, block := range m.ContentBlocks {
		if block.Type == "image" {
			return true
		}
	}
	return false
}

// HasAudio returns true if the message contains any audio content blocks
func (m *Message) HasAudio() bool {
	for _, block := range m.ContentBlocks {
		if block.Type == "audio" {
			return true
		}
	}
	return false
}

// HasMedia returns true if the message contains any media content blocks
func (m *Message) HasMedia() bool {
	for _, block := range m.ContentBlocks {
		if block.Type == "image" || block.Type == "audio" {
			return true
		}
	}
	return false
}
