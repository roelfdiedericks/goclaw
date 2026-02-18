package actions

import (
	"fmt"
	"sort"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Handler processes an action and returns a result
type Handler func(Action) Result

// componentActions holds handlers for a single component
type componentActions struct {
	handlers map[string]Handler
}

var (
	registry   = make(map[string]*componentActions)
	registryMu sync.RWMutex
)

// Register adds a handler for a component action
func Register(component, action string, handler Handler) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if registry[component] == nil {
		registry[component] = &componentActions{
			handlers: make(map[string]Handler),
		}
	}
	registry[component].handlers[action] = handler
	L_debug("actions: registered", "component", component, "action", action)
}

// Unregister removes a handler for a component action
func Unregister(component, action string) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if ca := registry[component]; ca != nil {
		delete(ca.handlers, action)
		if len(ca.handlers) == 0 {
			delete(registry, component)
		}
	}
}

// UnregisterComponent removes all handlers for a component
func UnregisterComponent(component string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, component)
}

// dispatch routes an action to its handler
func dispatch(action Action) {
	L_info("actions: dispatch",
		"component", action.Component,
		"action", action.Name,
		"source", action.Source,
		"user", action.UserID,
	)

	registryMu.RLock()
	ca := registry[action.Component]
	var handler Handler
	if ca != nil {
		handler = ca.handlers[action.Name]
	}
	registryMu.RUnlock()

	var result Result

	if ca == nil {
		result = Result{
			Error:   fmt.Errorf("%w: %s", ErrNoHandler, action.Component),
			Message: fmt.Sprintf("component '%s' not available (service not running?)", action.Component),
		}
	} else if handler == nil {
		result = Result{
			Error:   fmt.Errorf("%w: %s.%s", ErrUnknownAction, action.Component, action.Name),
			Message: fmt.Sprintf("unknown action '%s' for component '%s'", action.Name, action.Component),
		}
	} else {
		// Execute handler
		result = handler(action)
	}

	// Send result if channel provided
	if action.Result != nil {
		select {
		case action.Result <- result:
		default:
			L_warn("actions: result channel full/closed",
				"component", action.Component,
				"action", action.Name,
			)
		}
	}
}

// ListComponents returns all registered component names
func ListComponents() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListActions returns all action names for a component
func ListActions(component string) []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	ca := registry[component]
	if ca == nil {
		return nil
	}

	names := make([]string, 0, len(ca.handlers))
	for name := range ca.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasHandler returns true if a handler is registered for the action
func HasHandler(component, action string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()

	ca := registry[component]
	if ca == nil {
		return false
	}
	_, ok := ca.handlers[action]
	return ok
}
