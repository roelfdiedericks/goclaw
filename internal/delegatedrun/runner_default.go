package delegatedrun

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

type DefaultRunner struct {
	exec    ExecuteFunc
	reg     Registry
	emitter Emitter
	laneSem chan struct{}

	mu     sync.Mutex
	active map[string]*activeRun
}

func NewDefaultRunner(exec ExecuteFunc, reg Registry, emitter Emitter) *DefaultRunner {
	return NewDefaultRunnerWithConcurrency(exec, reg, emitter, 0)
}

func NewDefaultRunnerWithConcurrency(exec ExecuteFunc, reg Registry, emitter Emitter, maxConcurrent int) *DefaultRunner {
	var laneSem chan struct{}
	if maxConcurrent > 0 {
		laneSem = make(chan struct{}, maxConcurrent)
	}
	return &DefaultRunner{
		exec:    exec,
		reg:     reg,
		emitter: emitter,
		laneSem: laneSem,
		active:  make(map[string]*activeRun),
	}
}

func (r *DefaultRunner) Start(ctx context.Context, spec RunSpec) (string, error) {
	if r.exec == nil {
		return "", errors.New("delegated runner execute function is nil")
	}
	runID := uuid.NewString()
	startedAt := time.Now()

	record := RunRecord{
		RunID:         runID,
		ParentRunID:   spec.ParentRunID,
		RequesterType: spec.RequesterType,
		RequesterID:   spec.RequesterID,
		RequesterSessionKey: spec.RequesterSessionKey,
		RequesterBindingState: spec.RequesterBindingState,
		RequesterBindingReason: spec.RequesterBindingReason,
		RequesterBindingUpdatedAt: spec.RequesterBindingUpdatedAt,
		RequesterBindingLastActiveAt: spec.RequesterBindingLastActiveAt,
		SessionKey:    spec.SessionKey,
		Purpose:       spec.Purpose,
		ResultMode:    spec.ResultMode,
		ExpectsCompletionMessage: spec.ExpectsCompletionMessage,
		DispatchOrder: spec.DispatchOrder,
		FallbackMode:  spec.FallbackMode,
		InjectMode:    spec.InjectMode,
		CompletionDispatchSeq: spec.CompletionDispatchSeq,
		CleanupState:  spec.CleanupState,
		DeferredReason: spec.DeferredReason,
		ContinuationState: spec.ContinuationState,
		ContinuationReason: spec.ContinuationReason,
		State:         RunStateQueued,
		StartedAt:     startedAt,
	}
	if r.reg != nil {
		_ = r.reg.Create(record)
	}
	if r.emitter != nil {
		_ = r.emitter.EmitStarted(ctx, StartedEvent{
			RunID:         runID,
			ParentRunID:   spec.ParentRunID,
			RequesterType: spec.RequesterType,
			RequesterID:   spec.RequesterID,
			State:         RunStateQueued,
			SessionKey:    spec.SessionKey,
			Purpose:       spec.Purpose,
			StartedAt:     startedAt,
			SchemaVersion: EventSchemaVersion,
		})
	}

	runCtx := ctx
	cancel := func() {}
	if spec.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}

	ar := &activeRun{
		cancel: cancel,
		done:   make(chan waitResult, 1),
		start:  startedAt,
		spec:   spec,
	}
	r.mu.Lock()
	r.active[runID] = ar
	r.mu.Unlock()
	L_debug("delegatedrun: queued", "runID", runID, "parentRunID", spec.ParentRunID, "requesterType", spec.RequesterType, "purpose", spec.Purpose, "timeoutSeconds", spec.TimeoutSeconds)

	go r.run(runCtx, runID, ar)
	return runID, nil
}

func (r *DefaultRunner) run(ctx context.Context, runID string, ar *activeRun) {
	defer ar.cancel()
	laneWaitStart := time.Now()
	if r.laneSem != nil {
		L_trace("delegatedrun: awaiting lane admission", "runID", runID, "laneCapacity", cap(r.laneSem))
	}
	releaseLane, acquired := r.acquireLane(ctx)
	if acquired {
		defer releaseLane()
		if r.laneSem != nil {
			L_debug("delegatedrun: lane admitted", "runID", runID, "waitMs", time.Since(laneWaitStart).Milliseconds())
		}
	} else {
		r.finishWithoutExecution(ctx, runID, ar, time.Now())
		return
	}
	if r.reg != nil {
		_ = r.reg.UpdateState(runID, RunStateRunning)
	}
	if r.emitter != nil {
		_ = r.emitter.EmitProgress(ctx, ProgressEvent{
			RunID:         runID,
			ParentRunID:   ar.spec.ParentRunID,
			RequesterType: ar.spec.RequesterType,
			RequesterID:   ar.spec.RequesterID,
			State:         RunStateRunning,
			Message:       "lane_admitted",
			At:            time.Now(),
			SchemaVersion: EventSchemaVersion,
		})
	}
	result, execErr := r.exec(ctx, ar.spec)
	now := time.Now()

	state := RunStateCompleted
	wErr := error(nil)
	if execErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state = RunStateTimeout
			if result.Error == "" {
				result.Error = ctx.Err().Error()
			}
		} else if errors.Is(ctx.Err(), context.Canceled) {
			state = RunStateCanceled
			if result.Error == "" {
				result.Error = ctx.Err().Error()
			}
		} else {
			state = RunStateFailed
			if result.Error == "" {
				result.Error = execErr.Error()
			}
		}
		wErr = execErr
	}

	if r.reg != nil {
		_ = r.reg.Complete(runID, result, state)
	}
	if r.emitter != nil {
		switch state {
		case RunStateCompleted:
			_ = r.emitter.EmitCompleted(ctx, CompletedEvent{
				RunID:         runID,
				ParentRunID:   ar.spec.ParentRunID,
				RequesterType: ar.spec.RequesterType,
				RequesterID:   ar.spec.RequesterID,
				State:         state,
				StartedAt:     ar.start,
				FinishedAt:    now,
				Usage:         result.Usage,
				SchemaVersion: EventSchemaVersion,
			})
		case RunStateCanceled:
			_ = r.emitter.EmitCanceled(ctx, CanceledEvent{
				RunID:         runID,
				ParentRunID:   ar.spec.ParentRunID,
				RequesterType: ar.spec.RequesterType,
				RequesterID:   ar.spec.RequesterID,
				State:         state,
				Error:         result.Error,
				StartedAt:     ar.start,
				FinishedAt:    now,
				SchemaVersion: EventSchemaVersion,
			})
		default:
			_ = r.emitter.EmitFailed(ctx, FailedEvent{
				RunID:         runID,
				ParentRunID:   ar.spec.ParentRunID,
				RequesterType: ar.spec.RequesterType,
				RequesterID:   ar.spec.RequesterID,
				State:         state,
				Error:         result.Error,
				StartedAt:     ar.start,
				FinishedAt:    now,
				SchemaVersion: EventSchemaVersion,
			})
		}
	}

	ar.done <- waitResult{result: result, state: state, err: wErr}
	close(ar.done)

	r.mu.Lock()
	delete(r.active, runID)
	r.mu.Unlock()
	L_debug("delegatedrun: completed", "runID", runID, "state", state)
}

func (r *DefaultRunner) acquireLane(ctx context.Context) (func(), bool) {
	if r.laneSem == nil {
		return func() {}, true
	}
	select {
	case r.laneSem <- struct{}{}:
		return func() { <-r.laneSem }, true
	case <-ctx.Done():
		return func() {}, false
	}
}

func (r *DefaultRunner) finishWithoutExecution(ctx context.Context, runID string, ar *activeRun, now time.Time) {
	state := RunStateCanceled
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		state = RunStateTimeout
	}
	result := RunResult{}
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	}
	if r.reg != nil {
		_ = r.reg.Complete(runID, result, state)
	}
	if r.emitter != nil {
		if state == RunStateCanceled {
			_ = r.emitter.EmitCanceled(ctx, CanceledEvent{
				RunID:         runID,
				ParentRunID:   ar.spec.ParentRunID,
				RequesterType: ar.spec.RequesterType,
				RequesterID:   ar.spec.RequesterID,
				State:         state,
				Error:         result.Error,
				StartedAt:     ar.start,
				FinishedAt:    now,
				SchemaVersion: EventSchemaVersion,
			})
		} else {
			_ = r.emitter.EmitFailed(ctx, FailedEvent{
				RunID:         runID,
				ParentRunID:   ar.spec.ParentRunID,
				RequesterType: ar.spec.RequesterType,
				RequesterID:   ar.spec.RequesterID,
				State:         state,
				Error:         result.Error,
				StartedAt:     ar.start,
				FinishedAt:    now,
				SchemaVersion: EventSchemaVersion,
			})
		}
	}
	ar.done <- waitResult{result: result, state: state, err: ctx.Err()}
	close(ar.done)
	L_warn("delegatedrun: finished without execution", "runID", runID, "state", state, "error", result.Error)
	r.mu.Lock()
	delete(r.active, runID)
	r.mu.Unlock()
}

func (r *DefaultRunner) Cancel(runID string) error {
	r.mu.Lock()
	ar, ok := r.active[runID]
	r.mu.Unlock()
	if !ok {
		L_debug("delegatedrun: cancel ignored run not active", "runID", runID)
		return ErrRunNotFound
	}
	L_info("delegatedrun: cancel requested", "runID", runID)
	ar.cancel()
	return nil
}

func (r *DefaultRunner) Wait(ctx context.Context, runID string) (RunResult, RunState, error) {
	r.mu.Lock()
	ar, ok := r.active[runID]
	r.mu.Unlock()
	if !ok {
		if r.reg != nil {
			rec, found := r.reg.Get(runID)
			if found {
				return rec.Result, rec.State, nil
			}
		}
		return RunResult{}, "", ErrRunNotFound
	}

	select {
	case <-ctx.Done():
		return RunResult{}, "", ctx.Err()
	case wr := <-ar.done:
		return wr.result, wr.state, wr.err
	}
}

