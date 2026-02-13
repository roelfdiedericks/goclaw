package session

import (
	"encoding/json"
	"fmt"
	"time"
)

// RecordType identifies the type of JSONL record
type RecordType string

const (
	RecordTypeSession        RecordType = "session"
	RecordTypeMessage        RecordType = "message"
	RecordTypeCompaction     RecordType = "compaction"
	RecordTypeCheckpoint     RecordType = "checkpoint" // GoClaw-only: rolling summaries
	RecordTypeModelChange    RecordType = "model_change"
	RecordTypeThinkingChange RecordType = "thinking_level_change"
	RecordTypeCustom         RecordType = "custom"
)

// BaseRecord contains fields common to all JSONL records
type BaseRecord struct {
	Type      RecordType `json:"type"`
	ID        string     `json:"id"`
	ParentID  *string    `json:"parentId"` // nil for first record
	Timestamp time.Time  `json:"timestamp"`
}

// SessionRecord is the first line of every session file
type SessionRecord struct {
	BaseRecord
	Version int    `json:"version"`
	CWD     string `json:"cwd"`
}

// MessageContent represents a content block in a message
type MessageContent struct {
	Type              string          `json:"type"`                        // "text", "thinking", "toolCall", "image"
	Text              string          `json:"text,omitempty"`              // for text type
	Thinking          string          `json:"thinking,omitempty"`          // for thinking type
	ThinkingSignature string          `json:"thinkingSignature,omitempty"` // for thinking type
	ID                string          `json:"id,omitempty"`                // for toolCall type
	Name              string          `json:"name,omitempty"`              // for toolCall type
	Arguments         json.RawMessage `json:"arguments,omitempty"`         // for toolCall type
	// Image fields (for type="image")
	ImageData     string `json:"data,omitempty"`     // Base64-encoded image data
	ImageMimeType string `json:"mimeType,omitempty"` // MIME type (e.g., "image/jpeg")
	ImageSource   string `json:"source,omitempty"`   // Source (e.g., "telegram", "browser")
}

// NewImageContent creates an image content block
func NewImageContent(data, mimeType, source string) MessageContent {
	return MessageContent{
		Type:          "image",
		ImageData:     data,
		ImageMimeType: mimeType,
		ImageSource:   source,
	}
}

// MessageUsage contains token usage information
type MessageUsage struct {
	Input       int   `json:"input"`
	Output      int   `json:"output"`
	CacheRead   int   `json:"cacheRead"`
	CacheWrite  int   `json:"cacheWrite"`
	TotalTokens int   `json:"totalTokens"`
	Cost        *Cost `json:"cost,omitempty"`
}

// Cost contains cost breakdown
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
	Total      float64 `json:"total"`
}

// MessageData contains the actual message payload
type MessageData struct {
	Role         string           `json:"role"` // "user", "assistant", "toolResult"
	Content      []MessageContent `json:"content"`
	Timestamp    int64            `json:"timestamp"`
	API          string           `json:"api,omitempty"`
	Provider     string           `json:"provider,omitempty"`
	Model        string           `json:"model,omitempty"`
	Usage        *MessageUsage    `json:"usage,omitempty"`
	StopReason   string           `json:"stopReason,omitempty"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	// Tool result fields
	ToolCallID string                 `json:"toolCallId,omitempty"`
	ToolName   string                 `json:"toolName,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	IsError    bool                   `json:"isError,omitempty"`
}

// MessageRecord represents a user/assistant/tool message
type MessageRecord struct {
	BaseRecord
	Message MessageData `json:"message"`
}

// CompactionDetails contains files read/modified before compaction
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// CompactionRecord marks history truncation
type CompactionRecord struct {
	BaseRecord
	Summary          string             `json:"summary"`
	FirstKeptEntryID string             `json:"firstKeptEntryId"`
	TokensBefore     int                `json:"tokensBefore"`
	Details          *CompactionDetails `json:"details,omitempty"`
	FromHook         bool               `json:"fromHook,omitempty"`
	FromCheckpoint   bool               `json:"fromCheckpoint,omitempty"` // GoClaw: true if summary came from checkpoint
}

// CheckpointData contains the structured checkpoint content (GoClaw-only)
type CheckpointData struct {
	Summary                  string   `json:"summary"`
	TokensAtCheckpoint       int      `json:"tokensAtCheckpoint"`
	MessageCountAtCheckpoint int      `json:"messageCountAtCheckpoint"`
	Topics                   []string `json:"topics,omitempty"`
	OpenQuestions            []string `json:"openQuestions,omitempty"`
	KeyDecisions             []string `json:"keyDecisions,omitempty"`
}

// CheckpointRecord is a rolling summary record (GoClaw-only)
type CheckpointRecord struct {
	BaseRecord
	Checkpoint CheckpointData `json:"checkpoint"`
}

// ModelChangeRecord marks a model switch
type ModelChangeRecord struct {
	BaseRecord
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// ThinkingLevelChangeRecord marks thinking mode change
type ThinkingLevelChangeRecord struct {
	BaseRecord
	ThinkingLevel string `json:"thinkingLevel"`
}

// CustomRecord for extension events
type CustomRecord struct {
	BaseRecord
	CustomType string                 `json:"customType"`
	Data       map[string]interface{} `json:"data"`
}

// SessionIndexEntry represents an entry in sessions.json
type SessionIndexEntry struct {
	SessionID       string                 `json:"sessionId"`
	UpdatedAt       int64                  `json:"updatedAt"` // Unix ms timestamp
	SystemSent      bool                   `json:"systemSent,omitempty"`
	AbortedLastRun  bool                   `json:"abortedLastRun,omitempty"`
	ChatType        string                 `json:"chatType,omitempty"`
	DeliveryContext map[string]interface{} `json:"deliveryContext,omitempty"`
	LastChannel     string                 `json:"lastChannel,omitempty"`
	Origin          map[string]interface{} `json:"origin,omitempty"`
	SessionFile     string                 `json:"sessionFile"`
	CompactionCount int                    `json:"compactionCount,omitempty"`
	TotalTokens     int                    `json:"totalTokens,omitempty"` // GoClaw: track token usage
	SkillsSnapshot  map[string]interface{} `json:"skillsSnapshot,omitempty"`
	FlushActioned   bool                   `json:"flushActioned,omitempty"` // GoClaw: true if agent wrote to memory at 90%
}

// SessionIndex is the sessions.json file structure
type SessionIndex map[string]*SessionIndexEntry

// Record is an interface for all record types
type Record interface {
	GetType() RecordType
	GetID() string
	GetParentID() *string
	GetTimestamp() time.Time
}

// Implement Record interface for all record types
func (r *BaseRecord) GetType() RecordType     { return r.Type }
func (r *BaseRecord) GetID() string           { return r.ID }
func (r *BaseRecord) GetParentID() *string    { return r.ParentID }
func (r *BaseRecord) GetTimestamp() time.Time { return r.Timestamp }

// GenerateRecordID creates a unique record ID with timestamp prefix
func GenerateRecordID() string {
	return fmt.Sprintf("%d_%08x", time.Now().UnixMilli(), time.Now().UnixNano()&0xFFFFFFFF)
}

// ParseRecord parses a JSON line into the appropriate record type
func ParseRecord(data []byte) (Record, error) {
	// First, parse just the type field
	var base struct {
		Type RecordType `json:"type"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("failed to parse record type: %w", err)
	}

	var record Record
	switch base.Type {
	case RecordTypeSession:
		var r SessionRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse session record: %w", err)
		}
		record = &r
	case RecordTypeMessage:
		var r MessageRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse message record: %w", err)
		}
		record = &r
	case RecordTypeCompaction:
		var r CompactionRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse compaction record: %w", err)
		}
		record = &r
	case RecordTypeCheckpoint:
		var r CheckpointRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse checkpoint record: %w", err)
		}
		record = &r
	case RecordTypeModelChange:
		var r ModelChangeRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse model change record: %w", err)
		}
		record = &r
	case RecordTypeThinkingChange:
		var r ThinkingLevelChangeRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse thinking level change record: %w", err)
		}
		record = &r
	case RecordTypeCustom:
		var r CustomRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse custom record: %w", err)
		}
		record = &r
	default:
		// Unknown type - parse as custom to be forward-compatible
		var r CustomRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("failed to parse unknown record type %q: %w", base.Type, err)
		}
		record = &r
	}

	return record, nil
}

// ExtractTextContent extracts plain text from message content blocks
func ExtractTextContent(content []MessageContent) string {
	var text string
	for _, c := range content {
		if c.Type == "text" && c.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += c.Text
		}
	}
	return text
}

// ExtractToolCalls extracts tool calls from message content blocks
func ExtractToolCalls(content []MessageContent) []MessageContent {
	var calls []MessageContent
	for _, c := range content {
		if c.Type == "toolCall" {
			calls = append(calls, c)
		}
	}
	return calls
}

// ExtractImages extracts image content blocks from message content
func ExtractImages(content []MessageContent) []MessageContent {
	var images []MessageContent
	for _, c := range content {
		if c.Type == "image" {
			images = append(images, c)
		}
	}
	return images
}

// HasImages returns true if the message content contains any images
func HasImages(content []MessageContent) bool {
	for _, c := range content {
		if c.Type == "image" {
			return true
		}
	}
	return false
}
