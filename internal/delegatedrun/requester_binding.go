package delegatedrun

import (
	"strings"
	"time"
)

const (
	RequesterBindingFocused   = "focused"
	RequesterBindingUnfocused = "unfocused"
)

// RequesterBinding captures the persistent requester route for delegated completion.
// It is derived from run record fields so completion continuation can resolve the
// same target session/channel without relying on transient tool-local state.
type RequesterBinding struct {
	Channel      string
	ChatID       string
	SessionKey   string
	State        string
	Reason       string
	UpdatedAt    *time.Time
	LastActiveAt *time.Time
}

type RequesterBindingUpdate struct {
	State        string
	Reason       string
	UpdatedAt    *time.Time
	LastActiveAt *time.Time
}

// BuildRequesterID composes the canonical requester ID format.
func BuildRequesterID(channel, chatID, fallbackUserID string) string {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel != "" || chatID != "" {
		return strings.TrimSpace(channel + ":" + chatID)
	}
	return strings.TrimSpace(fallbackUserID)
}

// ParseRequesterID extracts channel/chat components from requesterID when possible.
func ParseRequesterID(requesterID string) (channel, chatID string) {
	requesterID = strings.TrimSpace(requesterID)
	if requesterID == "" {
		return "", ""
	}
	parts := strings.SplitN(requesterID, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// ResolveRequesterBinding derives requester routing coordinates from persisted run record.
func ResolveRequesterBinding(rec RunRecord) RequesterBinding {
	channel, chatID := ParseRequesterID(rec.RequesterID)
	return RequesterBinding{
		Channel:      channel,
		ChatID:       chatID,
		SessionKey:   strings.TrimSpace(rec.RequesterSessionKey),
		State:        strings.TrimSpace(rec.RequesterBindingState),
		Reason:       strings.TrimSpace(rec.RequesterBindingReason),
		UpdatedAt:    rec.RequesterBindingUpdatedAt,
		LastActiveAt: rec.RequesterBindingLastActiveAt,
	}
}

func NormalizeRequesterBindingState(state string) string {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case RequesterBindingUnfocused:
		return RequesterBindingUnfocused
	default:
		return RequesterBindingFocused
	}
}

func DefaultRequesterBinding(spec *RunSpec, now time.Time) {
	if spec == nil {
		return
	}
	spec.RequesterBindingState = NormalizeRequesterBindingState(spec.RequesterBindingState)
	if spec.RequesterBindingUpdatedAt == nil {
		t := now
		spec.RequesterBindingUpdatedAt = &t
	}
}

func CanDirectDispatchForBinding(binding RequesterBinding, now time.Time) (bool, string) {
	_ = now // timer-based gating intentionally disabled (manual focus/unfocus only)
	state := NormalizeRequesterBindingState(binding.State)
	if state == RequesterBindingUnfocused {
		return false, "unfocused"
	}
	return true, ""
}

// BindingTelemetry returns normalized requester-binding telemetry fields
// used by status/API projections.
func BindingTelemetry(rec RunRecord, now time.Time) (ageSeconds int64, idleSeconds int64, canDirect bool, reason string) {
	if rec.RequesterBindingUpdatedAt != nil {
		ageSeconds = int64(now.Sub(*rec.RequesterBindingUpdatedAt).Seconds())
	}
	if rec.RequesterBindingLastActiveAt != nil {
		idleSeconds = int64(now.Sub(*rec.RequesterBindingLastActiveAt).Seconds())
	}
	canDirect, reason = CanDirectDispatchForBinding(ResolveRequesterBinding(rec), now)
	return ageSeconds, idleSeconds, canDirect, reason
}
