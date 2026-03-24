package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/session"
	toolsubagentspawn "github.com/roelfdiedericks/goclaw/internal/tools/subagent_spawn"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestInjectDelegatedReturnToSessionAddsSyntheticToolMessages(t *testing.T) {
	g := &Gateway{
		sessions: session.NewManager(),
	}
	u := &user.User{ID: "owner", Role: user.RoleOwner}

	err := g.InjectDelegatedReturnToSession(
		context.Background(),
		u,
		"http",
		"session-primary",
		"run-123",
		"Subagent run completed.\nrunId: run-123\nstate: completed",
		"",
	)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	msgs := g.sessions.Get("session-primary").GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected exactly 2 synthetic messages, got %d", len(msgs))
	}

	toolUse := msgs[0]
	if toolUse.Role != "tool_use" {
		t.Fatalf("expected first message role tool_use, got %q", toolUse.Role)
	}
	if toolUse.ToolUseID != "delegated_return:run-123" {
		t.Fatalf("unexpected tool_use id: %q", toolUse.ToolUseID)
	}
	if toolUse.ToolName != "subagent_spawn" {
		t.Fatalf("expected tool name subagent_spawn, got %q", toolUse.ToolName)
	}
	var input map[string]any
	if err := json.Unmarshal(toolUse.ToolInput, &input); err != nil {
		t.Fatalf("failed to parse tool input json: %v", err)
	}
	if input["runId"] != "run-123" {
		t.Fatalf("expected runId in tool input, got %#v", input)
	}

	toolResult := msgs[1]
	if toolResult.Role != "tool_result" {
		t.Fatalf("expected second message role tool_result, got %q", toolResult.Role)
	}
	if toolResult.ToolUseID != toolUse.ToolUseID {
		t.Fatalf("expected tool_result to reference same tool_use id, got %q", toolResult.ToolUseID)
	}
	if toolResult.Content == "" {
		t.Fatalf("expected non-empty tool_result content")
	}
	var resultPayload map[string]any
	if err := json.Unmarshal([]byte(toolResult.Content), &resultPayload); err != nil {
		t.Fatalf("expected tool_result payload JSON, got error: %v", err)
	}
	if resultPayload["schema"] != "delegated_completion.v1" {
		t.Fatalf("expected delegated completion schema, got %#v", resultPayload["schema"])
	}
	untrusted, ok := resultPayload["untrustedChildOutput"].(map[string]any)
	if !ok {
		t.Fatalf("expected untrustedChildOutput payload section, got %#v", resultPayload["untrustedChildOutput"])
	}
	if strings.TrimSpace(asString(untrusted["text"])) == "" {
		t.Fatalf("expected non-empty untrusted child output text")
	}
}

type delegatedGatewayRunnerStub struct{}

func (g *delegatedGatewayRunnerStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	events <- cron.AgentEndEvent{FinalText: "delegated completed via gateway"}
}

func (g *delegatedGatewayRunnerStub) GetOwnerUserID() string { return "owner" }

func (g *delegatedGatewayRunnerStub) InjectSystemEvent(ctx context.Context, text string) error { return nil }

func (g *delegatedGatewayRunnerStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}

func (g *delegatedGatewayRunnerStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}

func (g *delegatedGatewayRunnerStub) HandoffCronResult(ctx context.Context, jobName, result string) error { return nil }

func TestSubagentSpawnReturnToRequesterInjectsIntoRequesterSession(t *testing.T) {
	svc := cron.NewService(cron.NewStore("", ""), &delegatedGatewayRunnerStub{})
	svc.SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})

	g := &Gateway{
		sessions: session.NewManager(),
	}
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	ctx := types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:    "http",
		ChatID:     "chat-1",
		SessionKey: "session-primary",
		User:       u,
	})

	tool := toolsubagentspawn.NewTool(g.InjectDelegatedReturnToSession, nil)
	result, err := tool.Execute(ctx, json.RawMessage(`{
		"prompt":"integration reinjection test"
	}`))
	if err != nil {
		t.Fatalf("expected spawn success, got error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.GetText()), &payload); err != nil {
		t.Fatalf("failed to parse spawn result: %v", err)
	}
	runID, _ := payload["runId"].(string)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("missing runId in spawn result: %s", result.GetText())
	}

	deadline := time.Now().Add(2 * time.Second)
	var msgs []session.Message
	for time.Now().Before(deadline) {
		msgs = g.sessions.Get("session-primary").GetMessages()
		if len(msgs) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected synthetic messages in requester session, got %d", len(msgs))
	}

	toolUse := msgs[len(msgs)-2]
	toolResult := msgs[len(msgs)-1]
	if toolUse.Role != "tool_use" || toolResult.Role != "tool_result" {
		t.Fatalf("expected trailing tool_use/tool_result, got %q/%q", toolUse.Role, toolResult.Role)
	}
	if toolUse.ToolUseID != "delegated_return:"+runID {
		t.Fatalf("unexpected synthetic toolUseID: %q", toolUse.ToolUseID)
	}
	if toolResult.ToolUseID != toolUse.ToolUseID {
		t.Fatalf("expected tool_result to reference tool_use id, got %q vs %q", toolResult.ToolUseID, toolUse.ToolUseID)
	}
	var resultPayload map[string]any
	if err := json.Unmarshal([]byte(toolResult.Content), &resultPayload); err != nil {
		t.Fatalf("expected structured tool_result payload, got error: %v", err)
	}
	if resultPayload["kind"] != "task_completion" {
		t.Fatalf("expected task_completion payload kind, got %#v", resultPayload["kind"])
	}
	meta, ok := resultPayload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta object in tool_result payload")
	}
	if strings.TrimSpace(asString(meta["runId"])) == "" {
		t.Fatalf("expected runId in tool_result payload meta")
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

