package delegatedrun

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/bus"
)

func TestDefaultRunnerStateTransitionsAndCancelTimeout(t *testing.T) {
	t.Run("completed run updates state and result", func(t *testing.T) {
		reg := NewMemoryRegistry()
		r := NewDefaultRunner(
			func(ctx context.Context, spec RunSpec) (RunResult, error) {
				return RunResult{FinalText: "ok"}, nil
			},
			reg,
			nil,
		)

		runID, err := r.Start(context.Background(), RunSpec{
			RequesterType: "subagent",
			RequesterID:   "r1",
			SessionKey:    "session-1",
			Purpose:       "test",
		})
		if err != nil {
			t.Fatalf("start failed: %v", err)
		}
		result, state, waitErr := r.Wait(context.Background(), runID)
		if waitErr != nil {
			t.Fatalf("wait failed: %v", waitErr)
		}
		if state != RunStateCompleted {
			t.Fatalf("expected completed state, got %s", state)
		}
		if result.FinalText != "ok" {
			t.Fatalf("expected final text ok, got %q", result.FinalText)
		}
		rec, ok := reg.Get(runID)
		if !ok || rec.State != RunStateCompleted || rec.FinishedAt == nil {
			t.Fatalf("expected completed record with finishedAt, got %#v ok=%v", rec, ok)
		}
	})

	t.Run("failed run sets failed state", func(t *testing.T) {
		reg := NewMemoryRegistry()
		r := NewDefaultRunner(
			func(ctx context.Context, spec RunSpec) (RunResult, error) {
				return RunResult{}, errors.New("boom")
			},
			reg,
			nil,
		)

		runID, err := r.Start(context.Background(), RunSpec{
			RequesterType: "subagent",
			RequesterID:   "r2",
			SessionKey:    "session-2",
			Purpose:       "test",
		})
		if err != nil {
			t.Fatalf("start failed: %v", err)
		}
		_, state, waitErr := r.Wait(context.Background(), runID)
		if waitErr == nil || waitErr.Error() != "boom" {
			t.Fatalf("expected wait error boom, got %v", waitErr)
		}
		if state != RunStateFailed {
			t.Fatalf("expected failed state, got %s", state)
		}
	})

	t.Run("cancel during active execution yields canceled state", func(t *testing.T) {
		reg := NewMemoryRegistry()
		r := NewDefaultRunner(
			func(ctx context.Context, spec RunSpec) (RunResult, error) {
				<-ctx.Done()
				return RunResult{}, ctx.Err()
			},
			reg,
			nil,
		)

		runID, err := r.Start(context.Background(), RunSpec{
			RequesterType: "subagent",
			RequesterID:   "r3",
			SessionKey:    "session-3",
			Purpose:       "test",
		})
		if err != nil {
			t.Fatalf("start failed: %v", err)
		}
		if err := r.Cancel(runID); err != nil {
			t.Fatalf("cancel failed: %v", err)
		}
		_, state, waitErr := r.Wait(context.Background(), runID)
		if !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", waitErr)
		}
		if state != RunStateCanceled {
			t.Fatalf("expected canceled state, got %s", state)
		}
	})

	t.Run("timeout yields timeout state", func(t *testing.T) {
		reg := NewMemoryRegistry()
		r := NewDefaultRunner(
			func(ctx context.Context, spec RunSpec) (RunResult, error) {
				<-ctx.Done()
				return RunResult{}, ctx.Err()
			},
			reg,
			nil,
		)

		runID, err := r.Start(context.Background(), RunSpec{
			RequesterType:  "subagent",
			RequesterID:    "r4",
			SessionKey:     "session-4",
			Purpose:        "test",
			TimeoutSeconds: 1,
		})
		if err != nil {
			t.Fatalf("start failed: %v", err)
		}
		_, state, waitErr := r.Wait(context.Background(), runID)
		if !errors.Is(waitErr, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", waitErr)
		}
		if state != RunStateTimeout {
			t.Fatalf("expected timeout state, got %s", state)
		}
	})
}

func TestDefaultRunnerParallelMixedOutcomes(t *testing.T) {
	reg := NewMemoryRegistry()
	r := NewDefaultRunner(
		func(ctx context.Context, spec RunSpec) (RunResult, error) {
			switch spec.Prompt {
			case "ok":
				return RunResult{FinalText: "ok"}, nil
			case "fail":
				return RunResult{}, errors.New("failed on purpose")
			default:
				<-ctx.Done()
				return RunResult{}, ctx.Err()
			}
		},
		reg,
		nil,
	)

	okRun, err := r.Start(context.Background(), RunSpec{Prompt: "ok", SessionKey: "a", RequesterType: "subagent", RequesterID: "1"})
	if err != nil {
		t.Fatalf("start ok run failed: %v", err)
	}
	failRun, err := r.Start(context.Background(), RunSpec{Prompt: "fail", SessionKey: "b", RequesterType: "subagent", RequesterID: "2"})
	if err != nil {
		t.Fatalf("start fail run failed: %v", err)
	}
	cancelRun, err := r.Start(context.Background(), RunSpec{Prompt: "block", SessionKey: "c", RequesterType: "subagent", RequesterID: "3"})
	if err != nil {
		t.Fatalf("start cancel run failed: %v", err)
	}
	if err := r.Cancel(cancelRun); err != nil {
		t.Fatalf("cancel run failed: %v", err)
	}

	_, okState, okErr := r.Wait(context.Background(), okRun)
	_, failState, failErr := r.Wait(context.Background(), failRun)
	_, cancelState, cancelErr := r.Wait(context.Background(), cancelRun)

	if okErr != nil || okState != RunStateCompleted {
		t.Fatalf("ok run mismatch state=%s err=%v", okState, okErr)
	}
	if failState != RunStateFailed {
		t.Fatalf("fail run mismatch state=%s err=%v", failState, failErr)
	}
	if cancelState != RunStateCanceled {
		t.Fatalf("cancel run mismatch state=%s err=%v", cancelState, cancelErr)
	}
}

func TestDefaultRunnerConcurrencyLaneQueuesAndAdmitsSequentially(t *testing.T) {
	reg := NewMemoryRegistry()
	blockFirst := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	var mu sync.Mutex
	callOrder := make([]string, 0, 2)
	r := NewDefaultRunnerWithConcurrency(
		func(ctx context.Context, spec RunSpec) (RunResult, error) {
			mu.Lock()
			callOrder = append(callOrder, spec.Prompt)
			mu.Unlock()
			if spec.Prompt == "first" {
				<-blockFirst
				return RunResult{FinalText: "first-done"}, nil
			}
			secondStarted <- struct{}{}
			return RunResult{FinalText: "second-done"}, nil
		},
		reg,
		nil,
		1,
	)

	firstRun, err := r.Start(context.Background(), RunSpec{Prompt: "first", SessionKey: "s1", RequesterType: "subagent", RequesterID: "1"})
	if err != nil {
		t.Fatalf("start first failed: %v", err)
	}
	firstRunningDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(firstRunningDeadline) {
		firstRec, ok := reg.Get(firstRun)
		if ok && firstRec.State == RunStateRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	firstRec, _ := reg.Get(firstRun)
	if firstRec.State != RunStateRunning {
		t.Fatalf("expected first run running before starting second, got %s", firstRec.State)
	}

	secondRun, err := r.Start(context.Background(), RunSpec{Prompt: "second", SessionKey: "s2", RequesterType: "subagent", RequesterID: "2"})
	if err != nil {
		t.Fatalf("start second failed: %v", err)
	}

	// First should be running, second should still be queued behind lane capacity.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		firstRec, firstOK := reg.Get(firstRun)
		secondRec, secondOK := reg.Get(secondRun)
		if firstOK && secondOK && firstRec.State == RunStateRunning && secondRec.State == RunStateQueued {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	firstRec, _ = reg.Get(firstRun)
	secondRec, _ := reg.Get(secondRun)
	if firstRec.State != RunStateRunning {
		t.Fatalf("expected first run running, got %s", firstRec.State)
	}
	if secondRec.State != RunStateQueued {
		t.Fatalf("expected second run queued before first release, got %s", secondRec.State)
	}
	select {
	case <-secondStarted:
		t.Fatalf("expected second run not to start before first lane release")
	default:
	}

	close(blockFirst)
	if _, state, err := r.Wait(context.Background(), firstRun); err != nil || state != RunStateCompleted {
		t.Fatalf("first run wait mismatch state=%s err=%v", state, err)
	}
	if _, state, err := r.Wait(context.Background(), secondRun); err != nil || state != RunStateCompleted {
		t.Fatalf("second run wait mismatch state=%s err=%v", state, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(callOrder) != 2 || callOrder[0] != "first" || callOrder[1] != "second" {
		t.Fatalf("unexpected execution order: %#v", callOrder)
	}
}

func TestMemoryRegistryConcurrentCreateAndCompleteConsistency(t *testing.T) {
	reg := NewMemoryRegistry()
	const total = 64

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			runID := "run-" + strconv.Itoa(i)
			_ = reg.Create(RunRecord{
				RunID:      runID,
				State:      RunStateQueued,
				StartedAt:  time.Now(),
				SessionKey: "session",
			})
			_ = reg.UpdateState(runID, RunStateRunning)
			_ = reg.Complete(runID, RunResult{FinalText: "done"}, RunStateCompleted)
		}()
	}
	wg.Wait()

	runs := reg.List()
	if len(runs) != total {
		t.Fatalf("expected %d runs, got %d", total, len(runs))
	}
	for _, rec := range runs {
		if rec.State != RunStateCompleted {
			t.Fatalf("expected completed state for %s, got %s", rec.RunID, rec.State)
		}
		if rec.FinishedAt == nil {
			t.Fatalf("expected finishedAt for %s", rec.RunID)
		}
	}
}

func TestBusBridgeEmitterPublishesSchemaVersionAndRunFields(t *testing.T) {
	startCh := make(chan bus.Event, 1)
	doneCh := make(chan bus.Event, 1)
	startSub := bus.SubscribeEvent("delegated.run.started", func(ev bus.Event) {
		startCh <- ev
	})
	doneSub := bus.SubscribeEvent("delegated.run.completed", func(ev bus.Event) {
		doneCh <- ev
	})
	defer bus.UnsubscribeEvent(startSub)
	defer bus.UnsubscribeEvent(doneSub)

	em := NewBusBridgeEmitter()
	startAt := time.Now().UTC()
	finishAt := startAt.Add(2 * time.Second)

	_ = em.EmitStarted(context.Background(), StartedEvent{
		RunID:         "run-123",
		RequesterType: "subagent",
		RequesterID:   "req-1",
		State:         RunStateRunning,
		SessionKey:    "session-1",
		Purpose:       "test",
		StartedAt:     startAt,
		SchemaVersion: EventSchemaVersion,
	})
	_ = em.EmitCompleted(context.Background(), CompletedEvent{
		RunID:         "run-123",
		RequesterType: "subagent",
		RequesterID:   "req-1",
		State:         RunStateCompleted,
		StartedAt:     startAt,
		FinishedAt:    finishAt,
		SchemaVersion: EventSchemaVersion,
	})

	select {
	case ev := <-startCh:
		data, ok := ev.Data.(StartedEvent)
		if !ok {
			t.Fatalf("expected StartedEvent payload, got %#v", ev.Data)
		}
		if data.RunID != "run-123" || data.SchemaVersion != EventSchemaVersion || data.RequesterType == "" {
			t.Fatalf("unexpected started payload: %#v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for started event")
	}

	select {
	case ev := <-doneCh:
		data, ok := ev.Data.(CompletedEvent)
		if !ok {
			t.Fatalf("expected CompletedEvent payload, got %#v", ev.Data)
		}
		if data.RunID != "run-123" || data.SchemaVersion != EventSchemaVersion || data.FinishedAt.IsZero() {
			t.Fatalf("unexpected completed payload: %#v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for completed event")
	}
}
