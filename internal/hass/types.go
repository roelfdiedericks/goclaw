package hass

import (
	"encoding/json"
	"time"
)

// HAMessage represents a WebSocket message from/to Home Assistant
type HAMessage struct {
	ID      int             `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success *bool           `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *HAError        `json:"error,omitempty"`
	Event   *HAEvent        `json:"event,omitempty"`
}

// HAError represents an error response from Home Assistant
type HAError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HAEvent represents an event from Home Assistant
type HAEvent struct {
	EventType string      `json:"event_type"`
	Data      HAEventData `json:"data"`
	TimeFired string      `json:"time_fired"`
	Origin    string      `json:"origin,omitempty"`
	Context   *HAContext  `json:"context,omitempty"`
}

// HAContext represents the context of an event
type HAContext struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
}

// HAEventData represents the data payload of a state_changed event
type HAEventData struct {
	EntityID string   `json:"entity_id"`
	NewState *HAState `json:"new_state,omitempty"`
	OldState *HAState `json:"old_state,omitempty"`
}

// HAState represents the state of an entity
type HAState struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes,omitempty"`
	LastChanged string                 `json:"last_changed,omitempty"`
	LastUpdated string                 `json:"last_updated,omitempty"`
	Context     *HAContext             `json:"context,omitempty"`
}

// HAAuthMessage is the authentication message sent to Home Assistant
type HAAuthMessage struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token"`
}

// HASubscribeMessage is used to subscribe to events
type HASubscribeMessage struct {
	ID        int    `json:"id"`
	Type      string `json:"type"`
	EventType string `json:"event_type,omitempty"`
}

// HACommandMessage is a generic command message
type HACommandMessage struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

// Subscription represents a persisted event subscription
type Subscription struct {
	ID              string    `json:"id"`               // UUID
	Pattern         string    `json:"pattern"`          // glob pattern (mutually exclusive with Regex)
	Regex           string    `json:"regex"`            // regex pattern (mutually exclusive with Pattern)
	DebounceSeconds int       `json:"debounce_seconds"` // default: 5
	Prefix          string    `json:"prefix,omitempty"` // custom message prefix
	Full            bool      `json:"full"`             // true=full state object, false=brief (default)
	Wake            bool      `json:"wake"`             // trigger immediate heartbeat (default: true)
	CreatedAt       time.Time `json:"created_at"`
}

// SubscriptionFile represents the structure of hass-subscriptions.json
type SubscriptionFile struct {
	Subscriptions []Subscription `json:"subscriptions"`
}

// BriefEventPayload is the brief format for injected event messages
type BriefEventPayload struct {
	EntityID     string `json:"entity_id"`
	State        string `json:"state"`
	OldState     string `json:"old_state,omitempty"`
	FriendlyName string `json:"friendly_name,omitempty"`
	TimeFired    string `json:"time_fired"`
}

// FullEventPayload is the full format for injected event messages
type FullEventPayload struct {
	EntityID  string   `json:"entity_id"`
	NewState  *HAState `json:"new_state,omitempty"`
	OldState  *HAState `json:"old_state,omitempty"`
	TimeFired string   `json:"time_fired"`
}

// NewSubscription creates a new subscription with defaults
func NewSubscription(id string) Subscription {
	return Subscription{
		ID:              id,
		DebounceSeconds: 5,    // Default debounce
		Wake:            true, // Default wake
		CreatedAt:       time.Now(),
	}
}
