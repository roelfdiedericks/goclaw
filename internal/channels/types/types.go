// Package types defines shared types for the channels package.
// This is a separate package to avoid circular imports between
// channels/manager.go and the individual channel implementations.
package types

import (
	"context"
	"time"
)

// ChannelStatus represents the current state of a managed channel
type ChannelStatus struct {
	Running   bool      // Whether the channel is currently running
	Connected bool      // For channels with external connections (e.g., Telegram API)
	Error     error     // Last error if any
	StartedAt time.Time // When the channel was started
	Info      string    // Human-readable status info (e.g., "@botname", ":1337")
}

// ManagedChannel defines lifecycle management for channels.
// All channel implementations (telegram, http, tui) must implement this.
// Note: Implementations also need to implement gateway.Channel separately.
type ManagedChannel interface {
	// Name returns the channel's identifier
	Name() string

	// Start initializes and starts the channel
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel
	Stop() error

	// Reload applies new configuration at runtime.
	// The cfg parameter should be the channel's own Config type.
	Reload(cfg any) error

	// Status returns the current channel status
	Status() ChannelStatus
}
