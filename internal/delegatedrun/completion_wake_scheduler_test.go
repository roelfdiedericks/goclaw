package delegatedrun

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCompletionWakeScheduler_ReplacesExistingRunTimer(t *testing.T) {
	s := NewCompletionWakeScheduler()
	var calls int32

	first := time.Now().Add(200 * time.Millisecond)
	second := time.Now().Add(20 * time.Millisecond)
	s.Schedule("run-1", &first, func() {
		atomic.AddInt32(&calls, 1)
	})
	s.Schedule("run-1", &second, func() {
		atomic.AddInt32(&calls, 1)
	})

	time.Sleep(120 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly one wake callback, got %d", got)
	}
}

func TestCompletionWakeScheduler_ImmediateWhenPastWake(t *testing.T) {
	s := NewCompletionWakeScheduler()
	done := make(chan struct{}, 1)
	past := time.Now().Add(-1 * time.Second)
	s.Schedule("run-2", &past, func() {
		done <- struct{}{}
	})

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected immediate wake callback")
	}
}

