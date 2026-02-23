package gateway

import "github.com/roelfdiedericks/goclaw/internal/gateway/events"

// Re-export event types from gateway/events to maintain backward compatibility
type (
	AgentEvent        = events.AgentEvent
	EventAgentStart   = events.EventAgentStart
	EventTextDelta    = events.EventTextDelta
	EventToolStart    = events.EventToolStart
	EventToolEnd      = events.EventToolEnd
	EventAgentEnd     = events.EventAgentEnd
	EventAgentError   = events.EventAgentError
	EventThinking     = events.EventThinking
	EventThinkingDelta = events.EventThinkingDelta
	EventUserMessage  = events.EventUserMessage
)
