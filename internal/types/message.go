// Package types contains shared types used across multiple packages.
// This helps avoid import cycles between packages like llm and session.
package types

import (
	"encoding/json"
	"time"
)

// ImageAttachment represents an image attached to a message
type ImageAttachment struct {
	Data     string `json:"data"`             // Base64-encoded image data
	MimeType string `json:"mimeType"`         // MIME type (e.g., "image/jpeg")
	Source   string `json:"source,omitempty"` // Source (e.g., "telegram", "browser")
}

// Message represents a single message in a conversation.
// Used by both session management and LLM providers.
type Message struct {
	ID        string            `json:"id"`
	Role      string            `json:"role"`                  // "user", "assistant", "tool_use", "tool_result"
	Content   string            `json:"content"`
	Source    string            `json:"source"`                // "tui", "telegram", etc.
	Timestamp time.Time         `json:"timestamp"`
	ToolUseID string            `json:"toolUseId,omitempty"`   // for tool_use and tool_result
	ToolName  string            `json:"toolName,omitempty"`    // for tool_use
	ToolInput json.RawMessage   `json:"toolInput,omitempty"`   // for tool_use
	Images    []ImageAttachment `json:"images,omitempty"`      // Image attachments (for multimodal)
	Thinking  string            `json:"thinking,omitempty"`    // Reasoning/thinking content (Kimi, Deepseek, etc.)
}

// HasImages returns true if the message contains any images
func (m *Message) HasImages() bool {
	return len(m.Images) > 0
}
