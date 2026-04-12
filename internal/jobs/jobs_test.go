package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerStartCompletesAndLists(t *testing.T) {
	manager := &Manager{jobs: make(map[string]*jobEntry)}
	done := make(chan struct{})

	job := manager.Start(StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "start",
		InitialPhase:   "queued",
		InitialMessage: "queued",
		Cancelable:     true,
	}, func(ctx context.Context, reporter *Reporter) (interface{}, error) {
		reporter.Update("download", "downloading", 40)
		close(done)
		return map[string]any{"ok": true}, nil
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not run")
	}

	var status Status
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.Status(job.JobID)
		if !ok {
			t.Fatalf("expected job %s to exist", job.JobID)
		}
		status = current
		if status.State == StateCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if status.State != StateCompleted {
		t.Fatalf("expected completed state, got %s", status.State)
	}
	if status.ProgressPercent != 100 {
		t.Fatalf("expected progress 100, got %d", status.ProgressPercent)
	}
	items := manager.List("local_llm")
	if len(items) != 1 {
		t.Fatalf("expected 1 local_llm job, got %d", len(items))
	}
	if items[0].JobID != job.JobID {
		t.Fatalf("expected job %s in list, got %s", job.JobID, items[0].JobID)
	}
}

func TestManagerCancel(t *testing.T) {
	manager := &Manager{jobs: make(map[string]*jobEntry)}
	started := make(chan struct{})

	job := manager.Start(StartSpec{
		OwnerComponent: "local_llm",
		OwnerAction:    "ensure_runtime",
		Cancelable:     true,
	}, func(ctx context.Context, reporter *Reporter) (interface{}, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}

	if _, err := manager.Cancel(job.JobID); err != nil {
		t.Fatalf("cancel returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, ok := manager.Status(job.JobID)
		if !ok {
			t.Fatalf("expected job %s to exist", job.JobID)
		}
		if status.State == StateCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected canceled state")
}

func TestManagerCancelUnknownJob(t *testing.T) {
	manager := &Manager{jobs: make(map[string]*jobEntry)}
	_, err := manager.Cancel("missing")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}
