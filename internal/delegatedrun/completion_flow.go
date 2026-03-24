package delegatedrun

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultCompletionDispatchMaxAttempts = 3
	defaultCompletionDispatchBackoff     = 250 * time.Millisecond
	defaultDescendantWakePoll            = 500 * time.Millisecond
)

var ErrCompletionDeferredDescendants = errors.New("completion deferred: active descendants")

var completionRunGuards sync.Map // runID -> *sync.Mutex

// NotifyRunCompletion waits for run completion, builds a canonical envelope, and dispatches
// completion via the shared dispatch adapter.
func NotifyRunCompletion(
	ctx context.Context,
	runID string,
	waitRun func(context.Context, string) (RunResult, RunState, error),
	getRun func(string) (RunRecord, bool),
	listRuns func() []RunRecord,
	baseAdapter CompletionDispatchAdapter,
	dispatchByPath func(context.Context, DispatchPath, string, string, string) error,
) (CompletionEnvelope, error) {
	result, state, waitErr := waitRun(ctx, runID)
	return NotifyRecordedCompletion(ctx, runID, state, result, waitErr, getRun, listRuns, baseAdapter, dispatchByPath)
}

// NotifyRecordedCompletion dispatches a known completion outcome without waiting on runner state.
func NotifyRecordedCompletion(
	ctx context.Context,
	runID string,
	state RunState,
	result RunResult,
	waitErr error,
	getRun func(string) (RunRecord, bool),
	listRuns func() []RunRecord,
	baseAdapter CompletionDispatchAdapter,
	dispatchByPath func(context.Context, DispatchPath, string, string, string) error,
) (CompletionEnvelope, error) {
	unlock := lockCompletionRun(runID)
	defer unlock()
	recordPhase := func(phase, status, detail string) {
		if baseAdapter.RecordPhase != nil {
			_ = baseAdapter.RecordPhase(runID, phase, status, detail)
		}
	}
	setLifecycle := func(cleanupState, deferredReason, continuationState, continuationReason string, wakeAt *time.Time) {
		updateLifecycle(baseAdapter, runID, CompletionLifecycleUpdate{
			CleanupState:       cleanupState,
			DeferredReason:     deferredReason,
			ContinuationState:  continuationState,
			ContinuationReason: continuationReason,
			ContinuationWakeAt: wakeAt,
		})
	}

	env := BuildCompletionEnvelope(runID, state, result, waitErr)
	message := env.RenderMessage(3000)
	now := time.Now()
	setLifecycle("dispatching", "", "active", "", nil)

	policy := CompletionDispatchPolicy{
		Primary:  DispatchPathQueue,
		Fallback: DispatchPathNone,
	}
	if rec, ok := getRun(runID); ok {
		policy = completionPolicyFromRecord(rec)
	}
	if listRuns != nil {
		coordinator := NewGraphDescendantCoordinator(listRuns())
		active, count := coordinator.HasActiveDescendants(runID)
		if active {
			reason := "active_descendants=" + strconv.Itoa(count)
			wakeAt := time.Now().Add(defaultDescendantWakePoll)
			setLifecycle("deferred", "descendants_active", "waiting_descendants", reason, &wakeAt)
			recordPhase("descendant_gate", "deferred", reason)
			recordPhase("wake_continuation", "scheduled", reason)
			return env, fmt.Errorf("%w: %s", ErrCompletionDeferredDescendants, reason)
		}
		recordPhase("descendant_gate", "clear", "")
		recordPhase("wake_continuation", "resumed", "")
	}

	maxAttempts := defaultCompletionDispatchMaxAttempts
	lastErr := error(nil)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			recordPhase("dispatch_retry", "attempt", "attempt="+strconv.Itoa(attempt))
		}
		claimToken := ""
		if baseAdapter.ClaimDispatch != nil && getRun != nil {
			if rec, ok := getRun(runID); ok {
				seq := rec.CompletionDispatchSeq
				if seq <= 0 {
					seq = 1
				}
				claimToken = uuid.NewString()
				claimed, claimErr := baseAdapter.ClaimDispatch(runID, claimToken, seq)
				if claimErr != nil {
					recordPhase("dispatch_claim", "failed", claimErr.Error())
					return env, claimErr
				}
				if !claimed {
					recordPhase("dispatch_claim", "skipped", "owned_elsewhere")
					return env, nil
				}
				recordPhase("dispatch_claim", "claimed", "seq="+strconv.Itoa(seq))
			}
		}
		err := baseAdapter.Notify(ctx, runID, policy, func(ctx context.Context, path DispatchPath) error {
			return dispatchByPath(ctx, path, runID, message, env.ToolError)
		})
		if claimToken != "" && baseAdapter.ReleaseClaim != nil {
			_ = baseAdapter.ReleaseClaim(runID, claimToken)
		}
		if err == nil {
			setLifecycle("dispatched", "", "none", "", nil)
			return env, nil
		}
		lastErr = err
		if !IsRetryableCompletionDispatchError(err) {
			recordPhase("dispatch_retry", "give_up", "non_retryable="+err.Error())
			setLifecycle("expired", "non_retryable_dispatch_failure", "none", err.Error(), nil)
			return env, err
		}
		if attempt >= maxAttempts {
			break
		}
		backoff := time.Duration(attempt) * defaultCompletionDispatchBackoff
		wakeAt := now.Add(backoff)
		setLifecycle("deferred", "dispatch_retry_backoff", "scheduled_retry", err.Error(), &wakeAt)
		recordPhase("dispatch_retry", "backoff", "wait_ms="+strconv.Itoa(int(backoff.Milliseconds())))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			recordPhase("dispatch_retry", "interrupted", "error="+ctx.Err().Error())
			setLifecycle("deferred", "dispatch_retry_interrupted", "interrupted", ctx.Err().Error(), nil)
			return env, ctx.Err()
		case <-timer.C:
		}
		now = time.Now()
	}
	recordPhase("dispatch_retry", "give_up", "max_attempts="+strconv.Itoa(maxAttempts))
	if lastErr != nil {
		setLifecycle("expired", "dispatch_retry_exhausted", "exhausted", lastErr.Error(), nil)
	}
	return env, lastErr
}

func completionPolicyFromRecord(rec RunRecord) CompletionDispatchPolicy {
	policy := CompletionDispatchPolicy{
		Primary:  DispatchPathQueue,
		Fallback: DispatchPathNone,
	}
	if rec.ExpectsCompletionMessage {
		policy.Primary = DispatchPathDirect
		policy.Fallback = DispatchPathQueue
	}
	switch strings.TrimSpace(rec.DispatchOrder) {
	case "direct_first":
		policy.Primary = DispatchPathDirect
	case "queue_first":
		policy.Primary = DispatchPathQueue
	}
	switch strings.TrimSpace(rec.FallbackMode) {
	case "queue_fallback":
		policy.Fallback = DispatchPathQueue
	case "direct_fallback":
		policy.Fallback = DispatchPathDirect
	case "none":
		policy.Fallback = DispatchPathNone
	}
	return policy
}

func updateLifecycle(adapter CompletionDispatchAdapter, runID string, update CompletionLifecycleUpdate) {
	if adapter.UpdateLifecycle != nil {
		_ = adapter.UpdateLifecycle(runID, update)
	}
}

func lockCompletionRun(runID string) func() {
	key := strings.TrimSpace(runID)
	if key == "" {
		return func() {}
	}
	value, _ := completionRunGuards.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return func() {
		mu.Unlock()
	}
}
