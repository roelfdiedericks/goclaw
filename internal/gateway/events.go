package gateway

import "encoding/json"

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
	RunID     string          `json:"runId"`
	ToolName  string          `json:"toolName"`
	ToolID    string          `json:"toolId"`
	Status    string          `json:"status,omitempty"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content,omitempty"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	RawOutput json.RawMessage `json:"rawOutput,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Locations []ToolLocation  `json:"locations,omitempty"`
}

func (EventToolStart) agentEvent() {}

// ToolLocation identifies a file location touched by a tool.
type ToolLocation struct {
	Path string `json:"path"`
	Line *int64 `json:"line,omitempty"`
}

// EventToolProgress is emitted when a tool execution reports progress.
type EventToolProgress struct {
	RunID         string          `json:"runId"`
	ToolName      string          `json:"toolName"`
	ToolID        string          `json:"toolId"`
	Status        string          `json:"status,omitempty"`
	Result        string          `json:"result,omitempty"`
	DisplayResult string          `json:"displayResult,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	Meta          json.RawMessage `json:"meta,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Locations     []ToolLocation  `json:"locations,omitempty"`
}

func (EventToolProgress) agentEvent() {}

// EventToolEnd is emitted when a tool execution completes
type EventToolEnd struct {
	RunID         string          `json:"runId"`
	ToolName      string          `json:"toolName"`
	ToolID        string          `json:"toolId"`
	Status        string          `json:"status,omitempty"`
	Result        string          `json:"result"`
	DisplayResult string          `json:"displayResult,omitempty"` // Human-readable result for UI/debug (unwrapped)
	Error         string          `json:"error,omitempty"`
	DurationMs    int64           `json:"durationMs,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	Meta          json.RawMessage `json:"meta,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Locations     []ToolLocation  `json:"locations,omitempty"`
}

func (EventToolEnd) agentEvent() {}

// EventACPDriverExtension is emitted for ACP driver-specific extension events.
type EventACPDriverExtension struct {
	RunID        string          `json:"runId"`
	Driver       string          `json:"driver"`
	Method       string          `json:"method"`
	Interactive  bool            `json:"interactive"`
	SemanticKind string          `json:"semanticKind,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

func (EventACPDriverExtension) agentEvent() {}

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
