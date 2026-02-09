package types

import "context"

// EventInjector allows injecting system events and invoking the agent.
// This interface decouples the HASS manager from the Gateway implementation.
type EventInjector interface {
	// InjectSystemEvent injects a system message into the primary session.
	// The message will be visible to the agent on the next turn (wake=false).
	InjectSystemEvent(ctx context.Context, text string) error

	// InvokeAgent runs the agent with a message and delivers the response.
	// source identifies the caller (e.g. "hass_event", "guidance").
	// suppressPrefix, if non-empty, suppresses delivery if response starts with it.
	InvokeAgent(ctx context.Context, source, message, suppressPrefix string) error
}
