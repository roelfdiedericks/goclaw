package subagent_spawn

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type gatewayRunnerStub struct{}

func (g *gatewayRunnerStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	events <- cron.AgentEndEvent{FinalText: "delegated completed"}
}

func (g *gatewayRunnerStub) GetOwnerUserID() string { return "owner" }

func (g *gatewayRunnerStub) InjectSystemEvent(ctx context.Context, text string) error { return nil }

func (g *gatewayRunnerStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}

func (g *gatewayRunnerStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}

func (g *gatewayRunnerStub) HandoffCronResult(ctx context.Context, jobName, result string) error { return nil }

func setupDelegatedService(t *testing.T) *cron.Service {
	t.Helper()
	svc := cron.NewService(cron.NewStore("", ""), &gatewayRunnerStub{})
	svc.SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})
	return svc
}

func setupDelegatedServiceWithSQLite(t *testing.T) *cron.Service {
	t.Helper()
	svc := cron.NewService(cron.NewStore("", ""), &gatewayRunnerStub{})
	sqlitePath := filepath.Join(t.TempDir(), "delegated_runs_test.db")
	svc.SetDelegatedRunsEnabled(true, sqlitePath, delegatedrun.SpawnLimits{})
	return svc
}

func ownerToolContext() context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:    "http",
		ChatID:     "chat-1",
		SessionKey: "session-primary",
		User:       u,
	})
}

func ownerToolContextWithRunID(runID string) context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:    "http",
		ChatID:     "chat-1",
		SessionKey: "session-primary",
		RunID:      runID,
		User:       u,
	})
}

func extractRunID(t *testing.T, result *types.ToolResult) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse tool output JSON: %v", err)
	}
	runID, _ := payload["runId"].(string)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("missing runId in tool output: %s", result.GetText())
	}
	return runID
}

func TestReturnToRequesterPrimaryFailFallbackQueue(t *testing.T) {
	svc := setupDelegatedService(t)
	_ = svc // default singleton is used by the tool

	injectCalled := make(chan struct{}, 1)
	directCalled := atomic.Int32{}
	tool := NewTool(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			injectCalled <- struct{}{}
			return nil
		},
		func(ctx context.Context, u *user.User, source, message string) error {
			directCalled.Add(1)
			return fmt.Errorf("direct channel unavailable")
		},
	)

	input := json.RawMessage(`{
		"prompt":"test fallback"
	}`)
	result, err := tool.Execute(ownerToolContext(), input)
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)

	select {
	case <-injectCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected fallback queue dispatch to be invoked")
	}
	if directCalled.Load() == 0 {
		t.Fatalf("expected direct dispatch attempt before fallback")
	}

	rec, ok := svc.GetDelegatedRun(runID)
	if !ok {
		t.Fatalf("expected run record to exist for %s", runID)
	}
	if rec.CompletionDispatchKey != runID+":1" {
		t.Fatalf("expected completion dispatch key %s:1, got %q", runID, rec.CompletionDispatchKey)
	}
}

func TestReturnToRequesterDuplicateSuppressed(t *testing.T) {
	svc := setupDelegatedService(t)
	injectCount := atomic.Int32{}
	tool := NewTool(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			injectCount.Add(1)
			return nil
		},
		nil,
	)

	input := json.RawMessage(`{
		"prompt":"test dedupe"
	}`)
	result, err := tool.Execute(ownerToolContext(), input)
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if injectCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if injectCount.Load() != 1 {
		t.Fatalf("expected first injection to occur exactly once, got %d", injectCount.Load())
	}

	// Replay the completion path manually; dedupe should suppress second dispatch.
	tool.waitAndNotify(runID, svc, &user.User{ID: "owner", Role: user.RoleOwner}, "http", "session-primary")
	time.Sleep(100 * time.Millisecond)
	if injectCount.Load() != 1 {
		t.Fatalf("expected duplicate completion to be suppressed; injectCount=%d", injectCount.Load())
	}
}

func TestReturnToRequesterRecordsDispatchPhasesInSQLite(t *testing.T) {
	svc := setupDelegatedServiceWithSQLite(t)

	injectCalled := make(chan struct{}, 1)
	tool := NewTool(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			injectCalled <- struct{}{}
			return nil
		},
		func(ctx context.Context, u *user.User, source, message string) error {
			return fmt.Errorf("direct channel unavailable")
		},
	)

	result, err := tool.Execute(ownerToolContext(), json.RawMessage(`{
		"prompt":"test sqlite phases"
	}`))
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)

	select {
	case <-injectCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected queue fallback injection")
	}

	// Wait until dispatch key is marked.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := svc.GetDelegatedRun(runID)
		if ok && rec.CompletionDispatchKey == runID+":1" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	events := svc.ListDelegatedRunEvents(0, 1000)
	var sawDirectAttempt, sawDirectFailed, sawQueueAttempt, sawQueueSuccess, sawDispatchMarked bool
	for _, ev := range events {
		if ev.RunID != runID {
			continue
		}
		if ev.EventType == "dispatch_marked" {
			sawDispatchMarked = true
		}
		if ev.EventType != "dispatch_phase" {
			continue
		}
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			continue
		}
		phase, _ := payload["phase"].(string)
		status, _ := payload["status"].(string)
		if phase == "direct_primary" && status == "attempt" {
			sawDirectAttempt = true
		}
		if phase == "direct_primary" && status == "failed" {
			sawDirectFailed = true
		}
		if phase == "queue_fallback" && status == "attempt" {
			sawQueueAttempt = true
		}
		if phase == "queue_fallback" && status == "success" {
			sawQueueSuccess = true
		}
	}

	if !sawDirectAttempt || !sawDirectFailed || !sawQueueAttempt || !sawQueueSuccess || !sawDispatchMarked {
		t.Fatalf(
			"expected dispatch phase events and dispatch mark, got directAttempt=%v directFailed=%v queueAttempt=%v queueSuccess=%v dispatchMarked=%v",
			sawDirectAttempt, sawDirectFailed, sawQueueAttempt, sawQueueSuccess, sawDispatchMarked,
		)
	}
}

func TestSpawnAutoPropagatesParentRunIDFromSessionRun(t *testing.T) {
	svc := setupDelegatedService(t)
	tool := NewTool(nil, nil)

	parentRunID, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		Prompt:        "parent run",
		UserID:        "owner",
	})
	if err != nil {
		t.Fatalf("failed to create parent delegated run: %v", err)
	}
	_, _, _ = svc.WaitDelegatedRun(context.Background(), parentRunID)
	result, err := tool.Execute(ownerToolContextWithRunID(parentRunID), json.RawMessage(`{
		"prompt":"test parent auto propagation",
		"notifyOnComplete":false
	}`))
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)

	rec, ok := svc.GetDelegatedRun(runID)
	if !ok {
		t.Fatalf("expected delegated run record for %s", runID)
	}
	if rec.ParentRunID != parentRunID {
		t.Fatalf("expected parentRunID %q, got %q", parentRunID, rec.ParentRunID)
	}
}

func TestSpawnSkipsAutoParentWhenSessionRunNotDelegated(t *testing.T) {
	svc := setupDelegatedService(t)
	tool := NewTool(nil, nil)

	const nonDelegatedSessionRunID = "gateway-run-not-in-delegated-registry"
	result, err := tool.Execute(ownerToolContextWithRunID(nonDelegatedSessionRunID), json.RawMessage(`{
		"prompt":"test unknown parent skip",
		"notifyOnComplete":false
	}`))
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)
	rec, ok := svc.GetDelegatedRun(runID)
	if !ok {
		t.Fatalf("expected delegated run record for %s", runID)
	}
	if rec.ParentRunID != "" {
		t.Fatalf("expected empty parentRunID for non-delegated session run, got %q", rec.ParentRunID)
	}
}

func TestReturnToRequesterFailureAdvancesDispatchSequence(t *testing.T) {
	svc := setupDelegatedService(t)
	tool := NewTool(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			return fmt.Errorf("inject unavailable")
		},
		nil,
	)

	result, err := tool.Execute(ownerToolContext(), json.RawMessage(`{
		"prompt":"test dispatch seq advance"
	}`))
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}
	runID := extractRunID(t, result)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := svc.GetDelegatedRun(runID)
		if ok && rec.CompletionDispatchSeq >= 2 {
			if rec.CompletionDispatchKey != "" {
				t.Fatalf("expected no completion dispatch key on failed dispatch, got %q", rec.CompletionDispatchKey)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec, _ := svc.GetDelegatedRun(runID)
	t.Fatalf("expected completion dispatch sequence to advance for retry, got %d", rec.CompletionDispatchSeq)
}

