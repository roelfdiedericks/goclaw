package delegatedrun

import (
	"context"
	"fmt"
	"strings"
)

// CompletionDispatchAdapter moves completion dispatch lifecycle out of tool handlers.
type CompletionDispatchAdapter struct {
	GetRun         func(runID string) (RunRecord, bool)
	RecordPhase    func(runID, phase, status, detail string) error
	MarkDispatched func(runID, dispatchKey string) error
	ClaimDispatch  func(runID, claimToken string, seq int) (bool, error)
	ReleaseClaim   func(runID, claimToken string) error
	AdvanceSeq     func(runID string) (int, error)
	UpdateLifecycle func(runID string, update CompletionLifecycleUpdate) error
	CanDispatchPath func(rec RunRecord, path DispatchPath) (bool, string)
}

func (a CompletionDispatchAdapter) Notify(
	ctx context.Context,
	runID string,
	policy CompletionDispatchPolicy,
	dispatchPath func(ctx context.Context, path DispatchPath) error,
) error {
	if strings.TrimSpace(runID) == "" {
		return NewNonRetryableDispatchError(DispatchErrRunIDRequired, DispatchPathNone, "run ID is required", nil)
	}
	if a.GetRun == nil || a.RecordPhase == nil || a.MarkDispatched == nil || a.AdvanceSeq == nil || dispatchPath == nil {
		return NewNonRetryableDispatchError(DispatchErrAdapterMisconfigured, DispatchPathNone, "completion dispatch adapter not fully configured", nil)
	}

	rec, hasRec := a.GetRun(runID)
	completionSeq := 1
	if hasRec && rec.CompletionDispatchSeq > 0 {
		completionSeq = rec.CompletionDispatchSeq
	}
	completionKey := fmt.Sprintf("%s:%d", runID, completionSeq)
	if hasRec && strings.TrimSpace(rec.CompletionDispatchKey) == completionKey {
		_ = a.RecordPhase(runID, "dedupe_check", "skipped_duplicate", completionKey)
		return nil
	}

	primary := normalizeDispatchPath(policy.Primary)
	fallback := normalizeDispatchPath(policy.Fallback)
	if primary == DispatchPathNone {
		return NewNonRetryableDispatchError(DispatchErrPrimaryPathNone, DispatchPathNone, "primary dispatch path is none", nil)
	}

	primaryPhase := phaseLabel(primary, "primary")
	_ = a.RecordPhase(runID, primaryPhase, "attempt", "")
	primaryErr := error(nil)
	if hasRec && a.CanDispatchPath != nil {
		if ok, reason := a.CanDispatchPath(rec, primary); !ok {
			_ = a.RecordPhase(runID, primaryPhase, "skipped", "ineligible="+strings.TrimSpace(reason))
			primaryErr = NewNonRetryableDispatchError(DispatchErrPathIneligible, primary, strings.TrimSpace(reason), nil)
		}
	}
	if primaryErr == nil {
		primaryErr = WrapDispatchPathError(primary, dispatchPath(ctx, primary))
	}
	if primaryErr == nil {
		_ = a.RecordPhase(runID, primaryPhase, "success", "")
		return a.MarkDispatched(runID, completionKey)
	}
	_ = a.RecordPhase(runID, primaryPhase, "failed", primaryErr.Error())
	if fallback == DispatchPathNone {
		a.advanceForRetry(runID)
		return primaryErr
	}
	if fallback == primary {
		a.advanceForRetry(runID)
		return NewNonRetryableDispatchError(
			DispatchErrFallbackDuplicatesPrimary,
			fallback,
			"fallback path duplicates primary path",
			nil,
		)
	}

	fallbackPhase := phaseLabel(fallback, "fallback")
	_ = a.RecordPhase(runID, fallbackPhase, "attempt", "")
	fallbackErr := error(nil)
	if hasRec && a.CanDispatchPath != nil {
		if ok, reason := a.CanDispatchPath(rec, fallback); !ok {
			_ = a.RecordPhase(runID, fallbackPhase, "skipped", "ineligible="+strings.TrimSpace(reason))
			fallbackErr = NewNonRetryableDispatchError(DispatchErrPathIneligible, fallback, strings.TrimSpace(reason), nil)
		}
	}
	if fallbackErr == nil {
		fallbackErr = WrapDispatchPathError(fallback, dispatchPath(ctx, fallback))
	}
	if fallbackErr != nil {
		_ = a.RecordPhase(runID, fallbackPhase, "failed", fallbackErr.Error())
		a.advanceForRetry(runID)
		return FormatPrimaryFallbackDispatchError(primary, primaryErr, fallback, fallbackErr)
	}
	_ = a.RecordPhase(runID, fallbackPhase, "success", "")
	return a.MarkDispatched(runID, completionKey)
}

func (a CompletionDispatchAdapter) advanceForRetry(runID string) {
	if nextSeq, err := a.AdvanceSeq(runID); err == nil {
		_ = a.RecordPhase(runID, "dispatch_seq", "advanced_for_retry", fmt.Sprintf("next=%d", nextSeq))
	}
}
