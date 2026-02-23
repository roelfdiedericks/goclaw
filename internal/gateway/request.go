package gateway

import "github.com/roelfdiedericks/goclaw/internal/gateway/events"

// Re-export types from gateway/events to maintain backward compatibility
type (
	MediaCallback = events.MediaCallback
	AgentRequest  = events.AgentRequest
	HealthStatus  = events.HealthStatus
)
