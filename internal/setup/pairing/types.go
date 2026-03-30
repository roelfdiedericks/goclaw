package pairing

import "time"

// State is the normalized lifecycle state for channel pairing.
type State string

const (
	StateNotStarted State = "not_started"
	StateWaiting    State = "waiting"
	StatePaired     State = "paired"
	StateExpired    State = "expired"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

// BaseRequest identifies a setup-owned pairing session.
type BaseRequest struct {
	SessionID string `json:"sessionId"`
	Surface   string `json:"surface,omitempty"` // web-wizard, web-editor, tui-wizard, tui-editor
}

// StartRequest starts or resumes a pairing session.
type StartRequest struct {
	BaseRequest
}

// StatusRequest returns the current pairing state.
type StatusRequest struct {
	BaseRequest
}

// CancelRequest cancels an active pairing session.
type CancelRequest struct {
	BaseRequest
}

// Identity describes the resolved owner identity for a paired channel.
type Identity struct {
	Provider    string `json:"provider,omitempty"`
	ID          string `json:"id,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	FirstName   string `json:"firstName,omitempty"`
	LastName    string `json:"lastName,omitempty"`
	Phone       string `json:"phone,omitempty"`
	JID         string `json:"jid,omitempty"`
}

// Artifacts contains channel-specific pairing UI data.
type Artifacts struct {
	Code    string `json:"code,omitempty"`
	QRCode  string `json:"qrCode,omitempty"`
	QRLabel string `json:"qrLabel,omitempty"`
}

// Status is the normalized pairing status returned to setup surfaces.
type Status struct {
	Channel     string      `json:"channel"`
	SessionID   string      `json:"sessionId"`
	State       State       `json:"state"`
	Phase       string      `json:"phase,omitempty"`
	Message     string      `json:"message,omitempty"`
	StartedAt   time.Time   `json:"startedAt,omitempty"`
	UpdatedAt   time.Time   `json:"updatedAt,omitempty"`
	ExpiresAt   time.Time   `json:"expiresAt,omitempty"`
	PollAfterMs int         `json:"pollAfterMs,omitempty"`
	Identity    *Identity   `json:"identity,omitempty"`
	Artifacts   *Artifacts  `json:"artifacts,omitempty"`
	Details     interface{} `json:"details,omitempty"`
}

// IsTerminal reports whether the state no longer requires polling.
func (s Status) IsTerminal() bool {
	switch s.State {
	case StatePaired, StateExpired, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}
