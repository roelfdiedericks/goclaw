package types

import "context"

// EventInjector allows injecting system events and invoking the agent.
// This interface decouples the HASS manager from the Gateway implementation.
type EventInjector interface {
	// InjectSystemEvent injects a system message into the primary session.
	// The message will be visible to the agent on the next turn (wake=false).
	InjectSystemEvent(ctx context.Context, text string) error

	// InvokeAgent runs the agent with a prompt and delivers response to channels.
	// The response is suppressed if it starts with "EVENT_OK".
	// Used for wake=true events that need immediate agent processing.
	InvokeAgent(ctx context.Context, prompt string) error
}
