package channels

import "time"

// ChannelStatus represents the current state of a managed channel
type ChannelStatus struct {
	Running   bool      // Whether the channel is currently running
	Connected bool      // For channels with external connections (e.g., Telegram API)
	Error     error     // Last error if any
	StartedAt time.Time // When the channel was started
	Stats     any       // Channel-specific stats (optional)
}
