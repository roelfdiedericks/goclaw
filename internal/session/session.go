// Package session provides conversation session management.
package session

import (
	"encoding/json"
	"sync"
	"time"
)

// ImageAttachment represents an image attached to a message
type ImageAttachment struct {
	Data     string `json:"data"`             // Base64-encoded image data
	MimeType string `json:"mimeType"`         // MIME type (e.g., "image/jpeg")
	Source   string `json:"source,omitempty"` // Source (e.g., "telegram", "browser")
}

// Message represents a single message in a conversation
type Message struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`      // "user", "assistant", "tool_use", "tool_result"
	Content   string          `json:"content"`
	Source    string          `json:"source"`    // "tui", "telegram", etc.
	Timestamp time.Time       `json:"timestamp"`
	ToolUseID string          `json:"toolUseId,omitempty"` // for tool_use and tool_result
	ToolName  string          `json:"toolName,omitempty"`  // for tool_use
	ToolInput json.RawMessage `json:"toolInput,omitempty"` // for tool_use
	Images    []ImageAttachment `json:"images,omitempty"`  // Image attachments (for multimodal)
}

// HasImages returns true if the message contains any images
func (m *Message) HasImages() bool {
	return len(m.Images) > 0
}

// Session holds the conversation state for a single session
type Session struct {
	ID        string    `json:"id"`
	Key       string    `json:"key,omitempty"` // Session key (e.g., "agent:main:main")
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Token tracking
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`  // Current context size
	MaxTokens    int `json:"maxTokens"`    // Model's context window

	// Persistence
	SessionFile string `json:"-"` // Path to JSONL file
	LastRecordID *string `json:"-"` // ID of last record (for parentId)

	// Checkpoints & Compaction
	LastCheckpoint    *CheckpointRecord `json:"-"` // Most recent checkpoint
	CompactionCount   int               `json:"compactionCount"`
	FlushedThresholds map[int]bool      `json:"-"` // Track which flush thresholds have fired

	// Metadata
	FlushActioned bool `json:"flushActioned,omitempty"` // True if agent wrote to memory at 90%

	mu sync.RWMutex
}

// NewSession creates a new session with the given ID
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		Messages:  make([]Message, 0),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// AddUserMessage adds a user message to the session
func (s *Session) AddUserMessage(content, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		ID:        generateMessageID(),
		Role:      "user",
		Content:   content,
		Source:    source,
		Timestamp: time.Now(),
	})
	s.UpdatedAt = time.Now()
}

// AddUserMessageWithImages adds a user message with image attachments to the session
func (s *Session) AddUserMessageWithImages(content, source string, images []ImageAttachment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		ID:        generateMessageID(),
		Role:      "user",
		Content:   content,
		Source:    source,
		Images:    images,
		Timestamp: time.Now(),
	})
	s.UpdatedAt = time.Now()
}

// AddAssistantMessage adds an assistant message to the session
func (s *Session) AddAssistantMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		ID:        generateMessageID(),
		Role:      "assistant",
		Content:   content,
		Timestamp: time.Now(),
	})
	s.UpdatedAt = time.Now()
}

// AddToolUse adds a tool use message to the session
func (s *Session) AddToolUse(toolUseID, toolName string, input json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		ID:        generateMessageID(),
		Role:      "tool_use",
		ToolUseID: toolUseID,
		ToolName:  toolName,
		ToolInput: input,
		Timestamp: time.Now(),
	})
	s.UpdatedAt = time.Now()
}

// AddToolResult adds a tool result message to the session
func (s *Session) AddToolResult(toolUseID, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = append(s.Messages, Message{
		ID:        generateMessageID(),
		Role:      "tool_result",
		ToolUseID: toolUseID,
		Content:   result,
		Timestamp: time.Now(),
	})
	s.UpdatedAt = time.Now()
}

// GetMessages returns a copy of all messages
func (s *Session) GetMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := make([]Message, len(s.Messages))
	copy(messages, s.Messages)
	return messages
}

// MessageCount returns the number of messages in the session
func (s *Session) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Messages)
}

// Clear removes all messages from the session
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Messages = make([]Message, 0)
	s.InputTokens = 0
	s.OutputTokens = 0
	s.UpdatedAt = time.Now()
}

// UpdateTokens updates the token count for the session
func (s *Session) UpdateTokens(input, output int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.InputTokens += input
	s.OutputTokens += output
}

// SetTotalTokens sets the current total token count (from API response)
func (s *Session) SetTotalTokens(total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalTokens = total
}

// GetTotalTokens returns the current total token count
func (s *Session) GetTotalTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TotalTokens
}

// SetMaxTokens sets the model's context window size
func (s *Session) SetMaxTokens(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MaxTokens = max
}

// GetMaxTokens returns the model's context window size
func (s *Session) GetMaxTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MaxTokens
}

// GetContextUsage returns the current context usage as a percentage (0.0 to 1.0)
func (s *Session) GetContextUsage() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.MaxTokens == 0 {
		return 0
	}
	return float64(s.TotalTokens) / float64(s.MaxTokens)
}

// HasFlushedThreshold returns true if the given threshold has already fired
func (s *Session) HasFlushedThreshold(percent int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.FlushedThresholds == nil {
		return false
	}
	return s.FlushedThresholds[percent]
}

// MarkThresholdFlushed marks a threshold as fired
func (s *Session) MarkThresholdFlushed(percent int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.FlushedThresholds == nil {
		s.FlushedThresholds = make(map[int]bool)
	}
	s.FlushedThresholds[percent] = true
}

// ResetFlushedThresholds clears all flushed thresholds (called after compaction)
func (s *Session) ResetFlushedThresholds() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FlushedThresholds = make(map[int]bool)
}

// SetLastRecordID sets the ID of the last record for parentId linking
func (s *Session) SetLastRecordID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastRecordID = &id
}

// GetLastRecordID returns the ID of the last record
func (s *Session) GetLastRecordID() *string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastRecordID
}

// UserMessageCount returns the count of user messages (for checkpoint triggers)
func (s *Session) UserMessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, msg := range s.Messages {
		if msg.Role == "user" {
			count++
		}
	}
	return count
}

// generate a simple message ID using timestamp
func generateMessageID() string {
	return time.Now().Format("20060102150405.000000")
}
