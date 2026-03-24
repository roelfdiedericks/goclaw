package delegatedrun

import (
	"strconv"
	"strings"
	"time"
)

// CompletionLifecycleState tracks in-flight completion orchestration status.
type CompletionLifecycleState struct {
	DispatchKey string
	DispatchSeq int
	Deferred    bool
	DeferReason string
}

// CompletionLifecycleUpdate persists cleanup/defer/continuation lifecycle transitions.
type CompletionLifecycleUpdate struct {
	CleanupState       string
	DeferredReason     string
	ContinuationState  string
	ContinuationReason string
	ContinuationWakeAt *time.Time
}

// NextDispatchKey builds the idempotency key used for completion delivery attempts.
func (s CompletionLifecycleState) NextDispatchKey(runID string) string {
	seq := s.DispatchSeq
	if seq <= 0 {
		seq = 1
	}
	return runID + ":" + strconv.Itoa(seq)
}

// IsDuplicateDispatch returns true when the candidate dispatch key was already recorded.
func (s CompletionLifecycleState) IsDuplicateDispatch(candidate string) bool {
	return strings.TrimSpace(candidate) != "" && strings.TrimSpace(s.DispatchKey) == strings.TrimSpace(candidate)
}

