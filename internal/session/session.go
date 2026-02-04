// Package session provides conversation session management.
package session

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

// Ensure json is available for other code in this file
var _ = json.RawMessage{}

// Type aliases for shared types - allows session code to use session.Message
// while the actual definition lives in types package (avoiding import cycles)
type (
	ImageAttachment = types.ImageAttachment
	Message         = types.Message
)

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

	// Identity & Display
	IsGroupChat bool   `json:"-"` // True for group chats (affects user label display)
	agentName   string // Agent's display name (set via SetAgentName)

	mu sync.RWMutex
}

// NewSession creates a new session with the given ID
// The ID is also used as the session Key for storage operations
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		Key:       id, // Key = ID for storage operations (multi-user support)
		Messages:  make([]Message, 0),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// SetAgentName sets the agent's display name for label helpers
func (s *Session) SetAgentName(name string) {
	s.agentName = name
}

// StorageUserLabel returns the user label for storage/transcript indexing.
// Always returns the actual username for searchability.
func (s *Session) StorageUserLabel(userName string) string {
	if userName == "" {
		return "User"
	}
	return userName
}

// DisplayUserLabel returns the user label for display purposes.
// Returns "You" in 1:1 chats, actual username in group chats.
func (s *Session) DisplayUserLabel(userName string) string {
	if s.IsGroupChat && userName != "" {
		return userName
	}
	return "You"
}

// AgentLabel returns the agent's display name.
func (s *Session) AgentLabel() string {
	if s.agentName != "" {
		return s.agentName
	}
	return "GoClaw"
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

// TruncateMessages removes messages beyond the given count (for ephemeral runs like heartbeat)
func (s *Session) TruncateMessages(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if count >= 0 && count < len(s.Messages) {
		s.Messages = s.Messages[:count]
	}
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
