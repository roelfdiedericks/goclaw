package delegatedrun

import (
	"fmt"
	"strings"
)

const DefaultStatusLogDepth = 50
const MaxStatusLogDepth = 500

// EnsureSubagentOwner enforces owner-only access for delegated subagent controls.
func EnsureSubagentOwner(isOwner bool) error {
	if isOwner {
		return nil
	}
	return PolicyDenied(PolicyReasonNotOwner, "subagent tools are owner-only")
}

// NormalizeStatusAction resolves implied status action defaults and validates support.
func NormalizeStatusAction(action, runID string) (string, error) {
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		if strings.TrimSpace(runID) != "" {
			action = "info"
		} else {
			action = "list"
		}
	}
	switch action {
	case "list", "info", "log", "focus", "unfocus", "steer", "send":
		return action, nil
	default:
		return "", PolicyDenied(PolicyReasonUnsupportedAction, "valid actions: list, info, log, focus, unfocus, steer, send")
	}
}

// NormalizeStatusLogDepth applies defaults and validates bounds for log action.
func NormalizeStatusLogDepth(depth int) (int, error) {
	if depth <= 0 {
		depth = DefaultStatusLogDepth
	}
	if depth > MaxStatusLogDepth {
		return 0, PolicyDenied(PolicyReasonLogDepthExceedsMax, fmt.Sprintf("max logDepth is %d", MaxStatusLogDepth))
	}
	return depth, nil
}

// CancelControlInput is the incoming cancel/kill control shape before normalization.
type CancelControlInput struct {
	Mode    string
	Scope   string
	Cascade *bool
}

// CancelControlPolicy is the normalized cancel/kill mode and scope policy.
type CancelControlPolicy struct {
	Mode    string
	Scope   string
	Cascade bool
}

// NormalizeCancelControl resolves default mode/scope and cascade compatibility semantics.
func NormalizeCancelControl(in CancelControlInput) (CancelControlPolicy, error) {
	mode := strings.TrimSpace(strings.ToLower(in.Mode))
	if mode == "" {
		mode = "cancel"
	}
	if mode != "cancel" && mode != "kill" {
		return CancelControlPolicy{}, PolicyDenied(PolicyReasonUnsupportedMode, "valid modes: cancel, kill")
	}

	scope := strings.TrimSpace(strings.ToLower(in.Scope))
	if scope == "" {
		scope = "subtree"
	}
	if scope != "self" && scope != "subtree" {
		return CancelControlPolicy{}, PolicyDenied(PolicyReasonUnsupportedScope, "valid scopes: self, subtree")
	}

	cascade := scope == "subtree"
	if in.Cascade != nil {
		cascade = *in.Cascade
		if cascade {
			scope = "subtree"
		} else {
			scope = "self"
		}
	}
	return CancelControlPolicy{
		Mode:    mode,
		Scope:   scope,
		Cascade: cascade,
	}, nil
}

// ValidateCancelControlForRun enforces centralized cancel/kill constraints against a run.
func ValidateCancelControlForRun(rec RunRecord, control CancelControlPolicy) error {
	if !IsActiveState(rec.State) {
		return PolicyDenied(PolicyReasonRunNotActive, fmt.Sprintf("run state is %s", rec.State))
	}
	if control.Mode == "kill" && control.Scope == "subtree" {
		return PolicyDenied(PolicyReasonUnsafeKillScope, "kill mode only supports self scope")
	}
	if control.Scope == "subtree" && (rec.RequesterType == "cron" || rec.RequesterType == "heartbeat") {
		return PolicyDenied(PolicyReasonRestrictedScope, "subtree scope is blocked for cron/heartbeat roots")
	}
	return nil
}
