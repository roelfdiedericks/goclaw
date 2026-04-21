package cron

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
)

type delegatedStartRunnerStub struct {
	started  int
	lastSpec delegatedrun.RunSpec
}

func (r *delegatedStartRunnerStub) Start(ctx context.Context, spec delegatedrun.RunSpec) (string, error) {
	r.started++
	r.lastSpec = spec
	return "run-test", nil
}

func (r *delegatedStartRunnerStub) Cancel(runID string) error { return nil }

func (r *delegatedStartRunnerStub) Wait(ctx context.Context, runID string) (delegatedrun.RunResult, delegatedrun.RunState, error) {
	return delegatedrun.RunResult{}, delegatedrun.RunStateCompleted, nil
}

func TestStartDelegatedRunEnforcesPerParentChildLimit(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	now := time.Now()
	_ = reg.Create(delegatedrun.RunRecord{RunID: "parent", StartedAt: now})
	_ = reg.Create(delegatedrun.RunRecord{
		RunID:       "child-1",
		ParentRunID: "parent",
		State:       delegatedrun.RunStateRunning,
		StartedAt:   now,
	})
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
		delegatedLimits: delegatedrun.SpawnLimits{
			MaxActiveChildrenPerParent: 1,
		},
	}

	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		ParentRunID:   "parent",
		Prompt:        "test",
		UserID:        "owner",
	})
	if err == nil || !strings.Contains(err.Error(), "active child limit reached") {
		t.Fatalf("expected per-parent child limit error, got: %v", err)
	}
	if runner.started != 0 {
		t.Fatalf("expected runner start not called")
	}
}

func TestStartDelegatedRunEnforcesDepthLimit(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	now := time.Now()
	_ = reg.Create(delegatedrun.RunRecord{RunID: "root", StartedAt: now})
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
		delegatedLimits: delegatedrun.SpawnLimits{
			MaxSpawnDepth: 1,
		},
	}

	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		ParentRunID:   "root",
		Prompt:        "test",
		UserID:        "owner",
	})
	if err == nil || !strings.Contains(err.Error(), "maxSpawnDepth exceeded") {
		t.Fatalf("expected depth limit error, got: %v", err)
	}
	if runner.started != 0 {
		t.Fatalf("expected runner start not called")
	}
}

func TestStartDelegatedRunSkipsLimitsForCron(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:          runner,
		registry:        reg,
		delegatedLimits: delegatedrun.SpawnLimits{MaxConcurrentRuns: 1},
	}

	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "cron",
		Prompt:        "cron task",
		UserID:        "owner",
	})
	if err != nil {
		t.Fatalf("expected cron requester to bypass limits, got: %v", err)
	}
	if runner.started != 1 {
		t.Fatalf("expected runner start called exactly once, got %d", runner.started)
	}
}

func TestStartDelegatedRunAppliesDefaultTimeoutWhenUnset(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
		delegatedLimits: delegatedrun.SpawnLimits{
			DefaultTimeoutSeconds: 120,
		},
	}
	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		Prompt:        "test",
		UserID:        "owner",
	})
	if err != nil {
		t.Fatalf("expected start success, got: %v", err)
	}
	if runner.lastSpec.TimeoutSeconds != 120 {
		t.Fatalf("expected default timeout 120, got %d", runner.lastSpec.TimeoutSeconds)
	}
}

func TestStartDelegatedRunCapsTimeoutAtMax(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
		delegatedLimits: delegatedrun.SpawnLimits{
			MaxTimeoutSeconds: 60,
		},
	}
	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType:  "subagent",
		Prompt:         "test",
		UserID:         "owner",
		TimeoutSeconds: 600,
	})
	if err != nil {
		t.Fatalf("expected start success, got: %v", err)
	}
	if runner.lastSpec.TimeoutSeconds != 60 {
		t.Fatalf("expected timeout capped to 60, got %d", runner.lastSpec.TimeoutSeconds)
	}
}

func TestStartDelegatedRunDefaultsLLMPurposeByRequesterType(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
	}

	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		Prompt:        "test",
		UserID:        "owner",
	})
	if err != nil {
		t.Fatalf("expected subagent start success, got: %v", err)
	}
	if runner.lastSpec.LLMPurpose != "subagent" {
		t.Fatalf("expected default llmPurpose=subagent, got %q", runner.lastSpec.LLMPurpose)
	}

	_, err = svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "cron",
		Prompt:        "cron test",
		UserID:        "owner",
	})
	if err != nil {
		t.Fatalf("expected cron start success, got: %v", err)
	}
	if runner.lastSpec.LLMPurpose != "cron" {
		t.Fatalf("expected default llmPurpose=cron for cron requester, got %q", runner.lastSpec.LLMPurpose)
	}
}

func TestStartDelegatedRunNormalizesUnknownLLMPurposeToSubagent(t *testing.T) {
	reg := delegatedrun.NewMemoryRegistry()
	runner := &delegatedStartRunnerStub{}
	svc := &Service{
		runner:   runner,
		registry: reg,
	}

	_, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		Prompt:        "test",
		UserID:        "owner",
		LLMPurpose:    "research",
	})
	if err != nil {
		t.Fatalf("expected start success, got: %v", err)
	}
	if runner.lastSpec.LLMPurpose != "subagent" {
		t.Fatalf("expected unknown llmPurpose normalized to subagent, got %q", runner.lastSpec.LLMPurpose)
	}
}
