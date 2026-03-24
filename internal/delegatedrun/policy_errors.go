package delegatedrun

import (
	"fmt"
	"strings"
)

const (
	PolicyReasonNotOwner        = "not_owner"
	PolicyReasonUnsupportedAction = "unsupported_action"
	PolicyReasonUnsupportedMode = "unsupported_mode"
	PolicyReasonUnsupportedScope = "unsupported_scope"
	PolicyReasonRunNotActive    = "run_not_active"
	PolicyReasonUnsafeKillScope = "unsafe_kill_scope"
	PolicyReasonRestrictedScope = "restricted_scope"
	PolicyReasonLogDepthExceedsMax = "log_depth_exceeds_max"
)

// PolicyDenied returns a normalized policy denial error string.
// String format is intentionally retained for release compatibility.
func PolicyDenied(reason, detail string) error {
	reason = strings.TrimSpace(reason)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return fmt.Errorf("policy_denied:%s", reason)
	}
	return fmt.Errorf("policy_denied:%s:%s", reason, detail)
}

