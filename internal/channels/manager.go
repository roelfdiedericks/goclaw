package channels

import (
	"context"
	"sync"

	"github.com/roelfdiedericks/goclaw/internal/gateway"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

// ManagedChannel extends gateway.Channel with lifecycle management
type ManagedChannel interface {
	gateway.Channel

	// Start initializes and starts the channel
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel
	Stop() error

	// Reload applies new configuration at runtime
	Reload(cfg any) error

	// Status returns the current channel status
	Status() ChannelStatus
}

// Manager owns the lifecycle of all communication channels
type Manager struct {
	gw       *gateway.Gateway
	users    *user.Registry
	channels map[string]ManagedChannel
	mu       sync.RWMutex
}

// NewManager creates a new channel manager
func NewManager(gw *gateway.Gateway, users *user.Registry) *Manager {
	return &Manager{
		gw:       gw,
		users:    users,
		channels: make(map[string]ManagedChannel),
	}
}

// StartAll starts all enabled channels from config
func (m *Manager) StartAll(ctx context.Context, cfg Config) error {
	// Implementation will be added in Phase 5
	return nil
}

// StopAll gracefully shuts down all running channels
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, ch := range m.channels {
		if err := ch.Stop(); err != nil {
			// Log error but continue stopping others
			_ = err // TODO: proper logging
		}
		delete(m.channels, name)
	}
}

// RunTUI starts the TUI channel and blocks until it exits
// TUI is special - it takes over the terminal and blocks the main thread
func (m *Manager) RunTUI(ctx context.Context, cfg any) error {
	// Implementation will be added in Phase 5
	return nil
}

// Reload applies new configuration to a running channel
func (m *Manager) Reload(name string, cfg any) error {
	m.mu.RLock()
	ch, exists := m.channels[name]
	m.mu.RUnlock()

	if !exists {
		return nil // Channel not running, nothing to reload
	}

	return ch.Reload(cfg)
}

// Get returns a channel by name, or nil if not found
func (m *Manager) Get(name string) ManagedChannel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.channels[name]
}

// Status returns the status of all channels
func (m *Manager) Status() map[string]ChannelStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]ChannelStatus, len(m.channels))
	for name, ch := range m.channels {
		result[name] = ch.Status()
	}
	return result
}

// MessageChannels returns adapters for the message tool
// This will be called by main.go to register with the message tool
func (m *Manager) MessageChannels() map[string]any {
	// Implementation will be added in Phase 5
	// Returns map of channel name -> MessageChannel adapter
	return nil
}

// RegisterCommands registers bus commands for all channel types
func (m *Manager) RegisterCommands() {
	// Implementation will be added in Phase 9
	// Registers channels.telegram.test, channels.telegram.apply, etc.
}

// UnregisterCommands unregisters all bus commands
func (m *Manager) UnregisterCommands() {
	// Implementation will be added in Phase 9
}
