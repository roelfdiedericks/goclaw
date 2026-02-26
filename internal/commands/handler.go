package commands

import (
	"context"
)

// Handler wraps the command manager for backward compatibility.
// Deprecated: Use GetManager() directly instead.
type Handler struct {
	manager *Manager
}

// NewHandler creates a new command handler.
// This also initializes the global command manager if not already done.
func NewHandler(provider SessionProvider) *Handler {
	mgr := InitManager(provider)
	return &Handler{manager: mgr}
}

// Execute runs a command and returns the result
func (h *Handler) Execute(ctx context.Context, cmd string, sessionKey string, userID string) *CommandResult {
	return h.manager.Execute(ctx, cmd, sessionKey, userID)
}

// GetManager returns the underlying manager
func (h *Handler) GetManager() *Manager {
	return h.manager
}
