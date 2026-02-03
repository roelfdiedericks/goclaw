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

// EventThinking is emitted when the agent enters extended thinking mode
type EventThinking struct {
	RunID   string `json:"runId"`
	Content string `json:"content"`
}

func (EventThinking) agentEvent() {}
