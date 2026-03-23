package subagent_fanout

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type fanoutGatewayRunnerStub struct{}

func (g *fanoutGatewayRunnerStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	if strings.Contains(req.UserMsg, "Fanout child outcomes JSON:") {
		events <- cron.AgentEndEvent{FinalText: "synthesis: combined child outcomes"}
		return
	}
	if strings.Contains(req.UserMsg, "slow") {
		time.Sleep(120 * time.Millisecond)
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
	svc := cron.NewService(cron.NewStore("", ""), &fanoutGatewayRunnerStub{})
	svc.SetDelegatedRunsEnabled(true, "", limits)
}

func ownerFanoutContext() context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:    "http",
		ChatID:     "chat-1",
		SessionKey: "session-primary",
		RunID:      "parent-run-1",
		User:       u,
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
		Items []struct {
			Index  int    `json:"index"`
			Prompt string `json:"prompt"`
			RunID  string `json:"runId"`
			State  string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(payload.Items))
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
		Summary struct {
			Total       int `json:"total"`
			Completed   int `json:"completed"`
			SpawnFailed int `json:"spawnFailed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if payload.Summary.Total != 3 {
		t.Fatalf("expected total 3, got %d", payload.Summary.Total)
	}
	if payload.Summary.Completed != 3 {
		t.Fatalf("expected completed 3, got %d", payload.Summary.Completed)
	}
	if payload.Summary.SpawnFailed != 0 {
		t.Fatalf("expected spawnFailed 0, got %d", payload.Summary.SpawnFailed)
	}
}

func TestFanoutOptionalSynthesisPass(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	result, err := tool.Execute(ownerFanoutContext(), json.RawMessage(`{
		"prompts":["alpha","beta"],
		"parallelism":2,
		"synthesize":true
	}`))
	if err != nil {
		t.Fatalf("expected fanout with synthesis success, got error: %v", err)
	}
	var payload struct {
		Synthesis struct {
			OK            bool   `json:"ok"`
			RunID         string `json:"runId"`
			State         string `json:"state"`
			Text          string `json:"text"`
			Truncated     bool   `json:"truncated"`
			IncludedItems int    `json:"includedItems"`
			TotalItems    int    `json:"totalItems"`
		} `json:"synthesis"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.Synthesis.OK {
		t.Fatalf("expected synthesis ok=true")
	}
	if strings.TrimSpace(payload.Synthesis.RunID) == "" {
		t.Fatalf("expected synthesis runId")
	}
	if payload.Synthesis.State != string(delegatedrun.RunStateCompleted) {
		t.Fatalf("expected completed synthesis state, got %s", payload.Synthesis.State)
	}
	if !strings.Contains(payload.Synthesis.Text, "synthesis:") {
		t.Fatalf("expected synthesis text output, got %q", payload.Synthesis.Text)
	}
	if payload.Synthesis.TotalItems != 2 || payload.Synthesis.IncludedItems != 2 || payload.Synthesis.Truncated {
		t.Fatalf("expected full synthesis input metadata for 2 items, got %#v", payload.Synthesis)
	}
}

func TestFanoutSynthesisTruncatesLargeInputMetadata(t *testing.T) {
	setupFanoutService(t, delegatedrun.SpawnLimits{})
	tool := NewTool()
	// Enough items to exceed bounded synthesis input payload.
	prompts := make([]string, 70)
	for i := range prompts {
		prompts[i] = "prompt-" + strings.Repeat("x", 200)
	}
	b, _ := json.Marshal(map[string]any{
		"prompts":     prompts,
		"parallelism": 10,
		"synthesize":  true,
	})
	result, err := tool.Execute(ownerFanoutContext(), b)
	if err != nil {
		t.Fatalf("expected fanout synthesis success, got error: %v", err)
	}
	var payload struct {
		Synthesis struct {
			OK            bool `json:"ok"`
			Truncated     bool `json:"truncated"`
			IncludedItems int  `json:"includedItems"`
			TotalItems    int  `json:"totalItems"`
		} `json:"synthesis"`
	}
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse fanout payload: %v", err)
	}
	if !payload.Synthesis.OK {
		t.Fatalf("expected synthesis ok=true")
	}
	if !payload.Synthesis.Truncated {
		t.Fatalf("expected truncated synthesis metadata")
	}
	if payload.Synthesis.TotalItems != 70 {
		t.Fatalf("expected totalItems=70, got %d", payload.Synthesis.TotalItems)
	}
	if payload.Synthesis.IncludedItems <= 0 || payload.Synthesis.IncludedItems >= payload.Synthesis.TotalItems {
		t.Fatalf("expected includedItems to be bounded within total range, got %d", payload.Synthesis.IncludedItems)
	}
}

