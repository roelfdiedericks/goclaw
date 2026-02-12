// Package types contains shared types used across multiple packages.
package types

// OutboundMessage represents the final delivery payload to a channel.
// This is distinct from streaming AgentEvent types - it's the finished product.
//
// Note: For streaming, gateway/events.go AgentEvent types remain canonical.
// OutboundMessage is for final delivery after agent run completes.
type OutboundMessage struct {
	// Routing
	SessionKey string // Which session this belongs to
	Channel    string // Target channel ("telegram", "http", "tui", or "*" for all)
	ReplyTo    string // Channel-specific target (chat_id, conn_id)

	// Content
	Text   string   // Message text (may be empty if only media)
	Media  []string // File paths to send (images, files)
	Format string   // "text", "markdown" (hint for rendering)

	// Metadata
	Source    string // Original source that triggered this response
	RunID     string // Agent run ID for correlation
	Suppress  bool   // If true, don't actually deliver (response was suppressed)
	Error     string // If non-empty, this is an error response
}

// DeliveryResult tracks what happened when delivering a message.
type DeliveryResult struct {
	Channel   string // Which channel
	Success   bool   // Did delivery succeed?
	Error     string // Error message if failed
	MessageID string // Platform-specific message ID (for edits/reactions)
}

// DeliveryReport summarizes delivery across all channels.
type DeliveryReport struct {
	SessionKey string
	RunID      string
	FinalText  string           // Agent's final response text
	Results    []DeliveryResult // Delivery results per channel (empty in streaming mode)
	Suppressed bool             // Was the message suppressed entirely?
}

// Delivered returns true if at least one channel received the message.
func (r *DeliveryReport) Delivered() bool {
	for _, result := range r.Results {
		if result.Success {
			return true
		}
	}
	return false
}

// FailedChannels returns the names of channels that failed delivery.
func (r *DeliveryReport) FailedChannels() []string {
	var failed []string
	for _, result := range r.Results {
		if !result.Success {
			failed = append(failed, result.Channel)
		}
	}
	return failed
}
