// Package bus provides a unified message bus for GoClaw.
// Commands (request/response) and Events (pub/sub) can be triggered from
// TUI, web, chat commands, or CLI, and are handled by registered handlers.
package bus

import (
	"fmt"
	"sort"
	"sync"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Command represents a request to a component (request/response pattern)
type Command struct {
	Component string               // Target component: "transcript", "telegram", etc.
	Name      string               // Command name: "test", "apply", "reindex", etc.
	Payload   any                  // Optional payload (e.g., config struct)
	Source    string               // Origin: "tui", "web", "chat", "cli", "system"
	UserID    string               // Who triggered it (for audit)
	Result    chan<- CommandResult // Response channel (nil for fire-and-forget)
}

// CommandResult is the response from a command handler
type CommandResult struct {
	Success bool   // Whether the command succeeded
	Message string // Human-readable result message
	Data    any    // Optional structured data
	Error   error  // Error if failed
}

// CommandHandler processes a command and returns a result
type CommandHandler func(Command) CommandResult

// Error types
type busError string

func (e busError) Error() string { return string(e) }

const (
	ErrTimeout        busError = "command timed out"
	ErrBusFull        busError = "command bus full"
	ErrNoHandler      busError = "no handler registered"
	ErrUnknownCommand busError = "unknown command"
)

// componentCommands holds command handlers for a single component
type componentCommands struct {
	handlers map[string]CommandHandler
}

var (
	// commandBus is the global command channel
	commandBus               = make(chan Command, 100)
	commandDispatcherStarted sync.Once

	// commandRegistry maps components to their command handlers
	commandRegistry   = make(map[string]*componentCommands)
	commandRegistryMu sync.RWMutex
)

// --- Registration ---

// RegisterCommand adds a handler for a component command
func RegisterCommand(component, command string, handler CommandHandler) {
	commandRegistryMu.Lock()
	defer commandRegistryMu.Unlock()

	if commandRegistry[component] == nil {
		commandRegistry[component] = &componentCommands{
			handlers: make(map[string]CommandHandler),
		}
	}
	commandRegistry[component].handlers[command] = handler
	L_debug("bus: command registered", "component", component, "command", command)
}

// UnregisterCommand removes a handler for a component command
func UnregisterCommand(component, command string) {
	commandRegistryMu.Lock()
	defer commandRegistryMu.Unlock()

	if cc := commandRegistry[component]; cc != nil {
		delete(cc.handlers, command)
		if len(cc.handlers) == 0 {
			delete(commandRegistry, component)
		}
	}
}

// UnregisterComponent removes all command handlers for a component
func UnregisterComponent(component string) {
	commandRegistryMu.Lock()
	defer commandRegistryMu.Unlock()
	delete(commandRegistry, component)
}

// --- Send Commands ---

// SendCommand sends a command and waits for the result
// Returns error result if timeout or bus full
func SendCommand(component, name string, payload any) CommandResult {
	return SendCommandWithSource(component, name, payload, "unknown", "")
}

// SendCommandWithSource sends a command with source and user info
func SendCommandWithSource(component, name string, payload any, source, userID string) CommandResult {
	ensureCommandDispatcher()

	result := make(chan CommandResult, 1)
	cmd := Command{
		Component: component,
		Name:      name,
		Payload:   payload,
		Source:    source,
		UserID:    userID,
		Result:    result,
	}

	select {
	case commandBus <- cmd:
		// Command queued, wait for result
		select {
		case r := <-result:
			return r
		case <-time.After(30 * time.Second):
			return CommandResult{
				Error:   ErrTimeout,
				Message: "command timed out",
			}
		}
	default:
		return CommandResult{
			Error:   ErrBusFull,
			Message: "command bus full",
		}
	}
}

// SendCommandAsync sends a command without waiting for result
func SendCommandAsync(component, name string, payload any) {
	SendCommandAsyncWithSource(component, name, payload, "unknown", "")
}

// SendCommandAsyncWithSource sends a command with source info without waiting
func SendCommandAsyncWithSource(component, name string, payload any, source, userID string) {
	ensureCommandDispatcher()

	cmd := Command{
		Component: component,
		Name:      name,
		Payload:   payload,
		Source:    source,
		UserID:    userID,
		Result:    nil, // No result channel
	}

	select {
	case commandBus <- cmd:
		// Queued successfully
	default:
		L_warn("bus: command dropped (bus full)", "component", component, "command", name)
	}
}

// --- Dispatcher ---

// ensureCommandDispatcher starts the command dispatcher goroutine if not already running
func ensureCommandDispatcher() {
	commandDispatcherStarted.Do(func() {
		go runCommandDispatcher()
		L_debug("bus: command dispatcher started")
	})
}

// runCommandDispatcher processes commands from the bus
func runCommandDispatcher() {
	for cmd := range commandBus {
		dispatchCommand(cmd)
	}
}

// dispatchCommand routes a command to its handler
func dispatchCommand(cmd Command) {
	L_info("bus: command dispatch",
		"component", cmd.Component,
		"command", cmd.Name,
		"source", cmd.Source,
		"user", cmd.UserID,
	)

	commandRegistryMu.RLock()
	cc := commandRegistry[cmd.Component]
	var handler CommandHandler
	if cc != nil {
		handler = cc.handlers[cmd.Name]
	}
	commandRegistryMu.RUnlock()

	var result CommandResult

	if cc == nil {
		result = CommandResult{
			Error:   fmt.Errorf("%w: %s", ErrNoHandler, cmd.Component),
			Message: fmt.Sprintf("component '%s' not available (service not running?)", cmd.Component),
		}
	} else if handler == nil {
		result = CommandResult{
			Error:   fmt.Errorf("%w: %s.%s", ErrUnknownCommand, cmd.Component, cmd.Name),
			Message: fmt.Sprintf("unknown command '%s' for component '%s'", cmd.Name, cmd.Component),
		}
	} else {
		// Execute handler
		result = handler(cmd)
	}

	// Send result if channel provided
	if cmd.Result != nil {
		select {
		case cmd.Result <- result:
		default:
			L_warn("bus: result channel full/closed",
				"component", cmd.Component,
				"command", cmd.Name,
			)
		}
	}
}

// --- Introspection ---

// ListComponents returns all registered component names
func ListComponents() []string {
	commandRegistryMu.RLock()
	defer commandRegistryMu.RUnlock()

	names := make([]string, 0, len(commandRegistry))
	for name := range commandRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListCommands returns all command names for a component
func ListCommands(component string) []string {
	commandRegistryMu.RLock()
	defer commandRegistryMu.RUnlock()

	cc := commandRegistry[component]
	if cc == nil {
		return nil
	}

	names := make([]string, 0, len(cc.handlers))
	for name := range cc.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasCommandHandler returns true if a handler is registered for the command
func HasCommandHandler(component, command string) bool {
	commandRegistryMu.RLock()
	defer commandRegistryMu.RUnlock()

	cc := commandRegistry[component]
	if cc == nil {
		return false
	}
	_, ok := cc.handlers[command]
	return ok
}
