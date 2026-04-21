package subagent_fanout

import (
	"context"
	"encoding/json"
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

type fanoutGatewayRunnerStub struct{}

var legacyFanoutSummarizerCalls atomic.Int32

func (g *fanoutGatewayRunnerStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	if strings.Contains(req.UserMsg, "Fanout child outcomes JSON:") {
		legacyFanoutSummarizerCalls.Add(1)
		events <- cron.AgentEndEvent{FinalText: "synthesis: combined child outcomes"}
		return
	}
	if strings.Contains(req.UserMsg, "slow") {
		time.Sleep(120 * time.Millisecond)
	}
	if strings.Contains(req.UserMsg, "fail-child") {
		events <- cron.AgentErrorEvent{Error: "simulated worker failure"}
		return
	}
	events <- cron.AgentEndEvent{FinalText: "child:" + req.UserMsg}
}

func (g *fanoutGatewayRunnerStub) GetOwnerUserID() string { return "owner" }
func (g *fanoutGatewayRunnerStub) InjectSystemEvent(ctx context.Context, text string) error {
	return nil
}
func (g *fanoutGatewayRunnerStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}
func (g *fanoutGatewayRunnerStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}
func (g *fanoutGatewayRunnerStub) HandoffCronResult(ctx context.Context, jobName, result string) error {
	return nil
}

func setupFanoutService(t *testing.T, limits delegatedrun.SpawnLimits) {
	t.Helper()
	legacyFanoutSummarizerCalls.Store(0)
	svc := cron.NewService(cron.NewStore("", ""), &fanoutGatewayRunnerStub{})
	svc.SetDelegatedRunsEnabled(true, "", limits)
}

func ownerFanoutContext() context.Context {
	return ownerFanoutContextWithBudget(0, 200000, 4000)
}

func ownerFanoutContextWithBudget(totalTokens, maxTokens, reserveTokens int) context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:       "http",
		ChatID:        "chat-1",
		SessionKey:    "session-primary",
		RunID:         "parent-run-1",
		TotalTokens:   totalTokens,
		MaxTokens:     maxTokens,
		ReserveTokens: reserveTokens,
		User:          u,
	})
}

func TestFanoutDeterministicAggregationOrder(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["alpha","beta","gamma"],
		"parallelism":2
	}`))
	if err != nil {
		t.Fatalf("expected fanout success, got error: %v", err)
	}
	var payload struct {
		Overflow struct {
			Triggered bool `json:"triggered"`
		} `json:"overflow"`
		Items []struct {
			Index  int    `json:"index"`
			Prompt string `json:"prompt"`
			RunID  string `json:"runId"`
			State  string `json:"state"`
			Output string `json:"output"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(payload.Items))
	}
	if payload.Overflow.Triggered {
		t.Fatalf("did not expect overflow in default test payload")
	}
	expected := []string{"alpha", "beta", "gamma"}
	for i, item := range payload.Items {
		if item.Index != i {
			t.Fatalf("expected deterministic index %d, got %d", i, item.Index)
		}
		if item.Prompt != expected[i] {
			t.Fatalf("expected prompt %q at %d, got %q", expected[i], i, item.Prompt)
		}
		if strings.TrimSpace(item.RunID) == "" {
			t.Fatalf("expected runId for item %d", i)
		}
		if item.State != string(delegatedrun.RunStateCompleted) {
			t.Fatalf("expected completed state for item %d, got %s", i, item.State)
		}
		if !strings.Contains(item.Output, "child:"+expected[i]) {
			t.Fatalf("expected full child output for %q, got %q", expected[i], item.Output)
		}
	}
}

func TestFanoutRetriesPerParentLimitAndCompletesAllChildren(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{MaxActiveChildrenPerParent: 1})
	tool := NewTool()
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["slow child","second child","third child"],
		"parallelism":3,
		"parentRunId":"parent-fixed"
	}`))
	if err != nil {
		t.Fatalf("expected fanout success under retries, got error: %v", err)
	}
	var payload struct {
		Stats struct {
			Total       int `json:"total"`
			Completed   int `json:"completed"`
			SpawnFailed int `json:"spawnFailed"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if payload.Stats.Total != 3 {
		t.Fatalf("expected total 3, got %d", payload.Stats.Total)
	}
	if payload.Stats.Completed != 3 {
		t.Fatalf("expected completed 3, got %d", payload.Stats.Completed)
	}
	if payload.Stats.SpawnFailed != 0 {
		t.Fatalf("expected spawnFailed 0, got %d", payload.Stats.SpawnFailed)
	}
}

func TestFanoutReturnsPartialFailureAndSkipsSummaryWhenChildTimeouts(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["fail-child","beta","gamma"],
		"parallelism":3,
		"includeSummary":true
	}`))
	if err != nil {
		t.Fatalf("expected fanout tool result despite child timeout, got error: %v", err)
	}
	var payload struct {
		OK      bool   `json:"ok"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Stats   struct {
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"stats"`
		ExtraSummaryStatus struct {
			Requested bool   `json:"requested"`
			Included  bool   `json:"included"`
			Reason    string `json:"reason"`
			Message   string `json:"message"`
		} `json:"extraSummaryStatus"`
		ExtraSummary any `json:"extraSummary"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if payload.OK {
		t.Fatalf("expected ok=false on mixed child outcomes")
	}
	if payload.Status != "partial_failure" {
		t.Fatalf("expected partial_failure status, got %q", payload.Status)
	}
	if payload.Stats.Completed != 2 || payload.Stats.Failed != 1 {
		t.Fatalf("expected completed=2 failed=1, got %#v", payload.Stats)
	}
	if !strings.Contains(payload.Message, "failed=1") {
		t.Fatalf("expected status message to mention failure, got %q", payload.Message)
	}
	if !payload.ExtraSummaryStatus.Requested || payload.ExtraSummaryStatus.Included {
		t.Fatalf("expected extra summary to be requested but skipped, got %#v", payload.ExtraSummaryStatus)
	}
	if payload.ExtraSummaryStatus.Reason != "child_outcomes_unhealthy" {
		t.Fatalf("expected child_outcomes_unhealthy reason, got %q", payload.ExtraSummaryStatus.Reason)
	}
	if !strings.Contains(payload.ExtraSummaryStatus.Message, "failed, timed out") {
		t.Fatalf("expected unhealthy-child summary skip message, got %q", payload.ExtraSummaryStatus.Message)
	}
	if payload.ExtraSummary != nil {
		t.Fatalf("expected no extraSummary when child outcomes are unhealthy")
	}
}

func TestFanoutOptionalSynthesisPass(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["alpha","beta"],
		"parallelism":2,
		"includeSummary":true
	}`))
	if err != nil {
		t.Fatalf("expected fanout with synthesis success, got error: %v", err)
	}
	var payload struct {
		ExtraSummaryStatus struct {
			Requested bool `json:"requested"`
			Included  bool `json:"included"`
		} `json:"extraSummaryStatus"`
		ExtraSummary struct {
			OK    bool   `json:"ok"`
			RunID string `json:"runId"`
			State string `json:"state"`
			Text  string `json:"text"`
		} `json:"extraSummary"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.ExtraSummary.OK {
		t.Fatalf("expected extraSummary ok=true")
	}
	if !payload.ExtraSummaryStatus.Requested || !payload.ExtraSummaryStatus.Included {
		t.Fatalf("expected extraSummaryStatus to show included summary, got %#v", payload.ExtraSummaryStatus)
	}
	if strings.TrimSpace(payload.ExtraSummary.RunID) == "" {
		t.Fatalf("expected extraSummary runId")
	}
	if payload.ExtraSummary.State != string(delegatedrun.RunStateCompleted) {
		t.Fatalf("expected completed extraSummary state, got %s", payload.ExtraSummary.State)
	}
	if !strings.Contains(payload.ExtraSummary.Text, "synthesis:") {
		t.Fatalf("expected summary text output, got %q", payload.ExtraSummary.Text)
	}
}

func TestFanoutSkipsPartialSummaryWhenSummaryInputWouldTruncate(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	// Enough items to exceed bounded synthesis input payload.
	prompts := make([]string, 70)
	for i := range prompts {
		prompts[i] = "prompt-" + strings.Repeat("x", 200)
	}
	b, _ := json.Marshal(map[string]any{
		"prompts":        prompts,
		"parallelism":    10,
		"includeSummary": true,
	})
	result, err := tool.Execute(ownerFanoutContext(), b)
	if err != nil {
		t.Fatalf("expected fanout synthesis success, got error: %v", err)
	}
	var payload struct {
		ExtraSummaryStatus struct {
			Requested     bool   `json:"requested"`
			Included      bool   `json:"included"`
			Reason        string `json:"reason"`
			Message       string `json:"message"`
			IncludedItems int    `json:"includedItems"`
			TotalItems    int    `json:"totalItems"`
		} `json:"extraSummaryStatus"`
		ExtraSummary *struct {
			Text string `json:"text"`
		} `json:"extraSummary"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.ExtraSummaryStatus.Requested {
		t.Fatalf("expected extraSummaryStatus requested=true")
	}
	if payload.ExtraSummaryStatus.Included {
		t.Fatalf("expected partial summary to be skipped, got %#v", payload.ExtraSummaryStatus)
	}
	if payload.ExtraSummaryStatus.Reason != "partial_summary_skipped" {
		t.Fatalf("expected partial_summary_skipped reason, got %q", payload.ExtraSummaryStatus.Reason)
	}
	if payload.ExtraSummaryStatus.TotalItems != 70 {
		t.Fatalf("expected totalItems=70, got %d", payload.ExtraSummaryStatus.TotalItems)
	}
	if payload.ExtraSummaryStatus.IncludedItems <= 0 || payload.ExtraSummaryStatus.IncludedItems >= payload.ExtraSummaryStatus.TotalItems {
		t.Fatalf("expected includedItems to be bounded within total range, got %d", payload.ExtraSummaryStatus.IncludedItems)
	}
	if !strings.Contains(payload.ExtraSummaryStatus.Message, "skipped") {
		t.Fatalf("expected skip message, got %q", payload.ExtraSummaryStatus.Message)
	}
	if payload.ExtraSummary != nil {
		t.Fatalf("expected extraSummary to be omitted when summary would be partial")
	}
}

func TestFanoutOverflowReturnsHandlesForOmittedResults(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	longPrompts := []string{
		strings.Repeat("alpha-", 20),
		strings.Repeat("beta-", 180),
		strings.Repeat("gamma-", 180),
	}
	b, _ := json.Marshal(map[string]any{
		"prompts":        longPrompts,
		"parallelism":    3,
		"includeSummary": true,
	})
	result, err := tool.Execute(ownerFanoutContextWithBudget(120, 420, 40), b)
	if err != nil {
		t.Fatalf("expected fanout success with overflow, got error: %v", err)
	}
	var payload struct {
		Overflow struct {
			Triggered         bool `json:"triggered"`
			ReturnedInline    int  `json:"returnedInline"`
			OmittedFromInline int  `json:"omittedFromInline"`
			Inspect           struct {
				Tool   string `json:"tool"`
				Action string `json:"action"`
			} `json:"inspect"`
			Omitted []struct {
				RunID string `json:"runId"`
			} `json:"omitted"`
		} `json:"overflow"`
		Items []struct {
			Output              string `json:"output"`
			OutputOmittedReason string `json:"outputOmittedReason"`
		} `json:"items"`
		ExtraSummaryStatus struct {
			Requested bool `json:"requested"`
			Included  bool `json:"included"`
		} `json:"extraSummaryStatus"`
		ExtraSummary *struct {
			Text string `json:"text"`
		} `json:"extraSummary"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.Overflow.Triggered {
		t.Fatalf("expected overflow to trigger")
	}
	if payload.Overflow.OmittedFromInline <= 0 || len(payload.Overflow.Omitted) == 0 {
		t.Fatalf("expected omitted handles in overflow payload")
	}
	if payload.Overflow.Inspect.Tool != "subagent_status" || payload.Overflow.Inspect.Action != "info" {
		t.Fatalf("expected inspect path to point to subagent_status info, got %#v", payload.Overflow.Inspect)
	}
	foundOmitted := false
	for _, item := range payload.Items {
		if item.OutputOmittedReason == "inline_budget" {
			foundOmitted = true
		}
	}
	if !foundOmitted {
		t.Fatalf("expected omitted handles in payload items, got %#v", payload.Items)
	}
	if !payload.ExtraSummaryStatus.Requested || !payload.ExtraSummaryStatus.Included {
		t.Fatalf("expected extraSummary to remain included on overflow when it covered all worker outputs, got %#v", payload.ExtraSummaryStatus)
	}
	if payload.ExtraSummary == nil || strings.TrimSpace(payload.ExtraSummary.Text) == "" {
		t.Fatalf("expected optional extra summary to remain present on overflow")
	}
}

func TestFanoutCompletionHandoffDoesNotUseLegacySummarizerRun(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	injectCalled := atomic.Int32{}
	tool := NewToolWithReturnToRequester(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			injectCalled.Add(1)
			return nil
		},
		nil,
	)
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["alpha","beta"],
		"parallelism":2,
		"notifyOnComplete":true
	}`))
	if err != nil {
		t.Fatalf("expected fanout success, got error: %v", err)
	}
	var payload struct {
		CompletionCallback struct {
			Enabled bool   `json:"enabled"`
			RunID   string `json:"runId"`
			Mode    string `json:"mode"`
		} `json:"completionCallback"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.CompletionCallback.Enabled || strings.TrimSpace(payload.CompletionCallback.RunID) == "" {
		t.Fatalf("expected completion callback metadata, got %#v", payload.CompletionCallback)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if injectCalled.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if injectCalled.Load() == 0 {
		t.Fatalf("expected completion handoff dispatch to run")
	}
	if legacyFanoutSummarizerCalls.Load() != 0 {
		t.Fatalf("expected legacy summarizer run count 0, got %d", legacyFanoutSummarizerCalls.Load())
	}
}

func TestFanoutDefaultsToImmediateOnlyEvenWhenCallbackPlumbingExists(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	injectCalled := atomic.Int32{}
	tool := NewToolWithReturnToRequester(
		func(ctx context.Context, u *user.User, source, sessionKey, runID, message, toolError string) error {
			injectCalled.Add(1)
			return nil
		},
		nil,
	)
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["alpha","beta"],
		"parallelism":2
	}`))
	if err != nil {
		t.Fatalf("expected fanout success, got error: %v", err)
	}
	var payload struct {
		NotifyOnComplete   bool `json:"notifyOnComplete"`
		CompletionCallback struct {
			Enabled bool `json:"enabled"`
		} `json:"completionCallback"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if payload.NotifyOnComplete {
		t.Fatalf("expected notifyOnComplete=false by default")
	}
	if payload.CompletionCallback.Enabled {
		t.Fatalf("expected completion callback disabled by default")
	}
	time.Sleep(150 * time.Millisecond)
	if injectCalled.Load() != 0 {
		t.Fatalf("expected no callback injection by default, got %d", injectCalled.Load())
	}
}
