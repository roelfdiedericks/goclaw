// Package actions provides a component action bus for GoClaw.
// Actions can be triggered from TUI, web, chat commands, or CLI,
// and are handled by registered component handlers.
package actions

import (
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Action represents a request to a component
type Action struct {
	Component string        // Target component: "transcript", "telegram", etc.
	Name      string        // Action name: "test", "apply", "reindex", etc.
	Payload   any           // Optional payload (e.g., config struct)
	Source    string        // Origin: "tui", "web", "chat", "cli", "system"
	UserID    string        // Who triggered it (for audit)
	Result    chan<- Result // Response channel (nil for fire-and-forget)
}

// Result is the response from an action handler
type Result struct {
	Success bool   // Whether the action succeeded
	Message string // Human-readable result message
	Data    any    // Optional structured data
	Error   error  // Error if failed
}

// Bus is the global action channel
var Bus = make(chan Action, 100)

var dispatcherStarted sync.Once

// ensureDispatcher starts the dispatcher goroutine if not already running
func ensureDispatcher() {
	dispatcherStarted.Do(func() {
		go runDispatcher()
		L_debug("actions: dispatcher started")
	})
}

// runDispatcher processes actions from the bus
func runDispatcher() {
	for action := range Bus {
		dispatch(action)
	}
}

// Send sends an action and waits for the result
// Returns error result if timeout or bus full
func Send(component, name string, payload any) Result {
	return SendWithSource(component, name, payload, "unknown", "")
}

// SendWithSource sends an action with source and user info
func SendWithSource(component, name string, payload any, source, userID string) Result {
	ensureDispatcher()

	result := make(chan Result, 1)
	action := Action{
		Component: component,
		Name:      name,
		Payload:   payload,
		Source:    source,
		UserID:    userID,
		Result:    result,
	}

	select {
	case Bus <- action:
		// Action queued, wait for result
		select {
		case r := <-result:
			return r
		case <-time.After(30 * time.Second):
			return Result{
				Error:   ErrTimeout,
				Message: "action timed out",
			}
		}
	default:
		return Result{
			Error:   ErrBusFull,
			Message: "action bus full",
		}
	}
}

// SendAsync sends an action without waiting for result
func SendAsync(component, name string, payload any) {
	SendAsyncWithSource(component, name, payload, "unknown", "")
}

// SendAsyncWithSource sends an action with source info without waiting
func SendAsyncWithSource(component, name string, payload any, source, userID string) {
	ensureDispatcher()

	action := Action{
		Component: component,
		Name:      name,
		Payload:   payload,
		Source:    source,
		UserID:    userID,
		Result:    nil, // No result channel
	}

	select {
	case Bus <- action:
		// Queued successfully
	default:
		L_warn("actions: dropped (bus full)", "component", component, "action", name)
	}
}

// Error types
type actionError string

func (e actionError) Error() string { return string(e) }

const (
	ErrTimeout       actionError = "action timed out"
	ErrBusFull       actionError = "action bus full"
	ErrNoHandler     actionError = "no handler registered"
	ErrUnknownAction actionError = "unknown action"
)
