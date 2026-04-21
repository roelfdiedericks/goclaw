package cron

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
)

type delegatedCancelRunnerStub struct {
	canceled []string
	errByID  map[string]error
}

func (s *delegatedCancelRunnerStub) Start(ctx context.Context, spec delegatedrun.RunSpec) (string, error) {
	return "", errors.New("not implemented")
}

func (s *delegatedCancelRunnerStub) Cancel(runID string) error {
	s.canceled = append(s.canceled, runID)
	if s.errByID != nil {
		if err, ok := s.errByID[runID]; ok {
			return err
		}
	}
	return nil
}

func (s *delegatedCancelRunnerStub) Wait(ctx context.Context, runID string) (delegatedrun.RunResult, delegatedrun.RunState, error) {
	return delegatedrun.RunResult{}, "", errors.New("not implemented")
}

func TestCancelDelegatedRunCascadeCancelsDescendantsAndParent(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	now := time.Now()
	records := []delegatedrun.RunRecord{
		{RunID: "parent", ParentRunID: "", State: delegatedrun.RunStateRunning, StartedAt: now},
		{RunID: "child-a", ParentRunID: "parent", State: delegatedrun.RunStateRunning, StartedAt: now},
		{RunID: "child-b", ParentRunID: "parent", State: delegatedrun.RunStateRunning, StartedAt: now},
		{RunID: "grandchild", ParentRunID: "child-a", State: delegatedrun.RunStateRunning, StartedAt: now},
	}
	for _, rec := range records {
		_ = reg.Create(rec)
	}

	runner := &delegatedCancelRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
	}

	if err := svc.CancelDelegatedRunCascade("parent"); err != nil {
		t.Fatalf("expected cascade cancel to succeed, got error: %v", err)
	}

	got := map[string]bool{}
	for _, id := range runner.canceled {
		got[id] = true
	}
	for _, want := range []string{"parent", "child-a", "child-b", "grandchild"} {
		if !got[want] {
			t.Fatalf("expected run %q to be canceled, canceled=%v", want, runner.canceled)
		}
	}
}

func TestCancelDelegatedRunCascadeIgnoresNotFoundForDescendants(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	now := time.Now()
	_ = reg.Create(delegatedrun.RunRecord{RunID: "parent", State: delegatedrun.RunStateRunning, StartedAt: now})
	_ = reg.Create(delegatedrun.RunRecord{RunID: "child", ParentRunID: "parent", State: delegatedrun.RunStateCompleted, StartedAt: now})

	runner := &delegatedCancelRunnerStub{
		errByID: map[string]error{
			"child": delegatedrun.ErrRunNotFound, // child already finished and no longer active
		},
	}
	svc := &Service{
		runner:   runner,
		registry: reg,
	}

	if err := svc.CancelDelegatedRunCascade("parent"); err != nil {
		t.Fatalf("expected cascade cancel to ignore descendant not-found, got error: %v", err)
	}
}

func TestCancelDelegatedRunCascadeReturnsNotFoundForUnknownRoot(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedCancelRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
	}

	err := svc.CancelDelegatedRunCascade("missing")
	if !errors.Is(err, delegatedrun.ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound, got: %v", err)
	}
}
