package subagent_status

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type statusGatewayStub struct {
	injectCalls atomic.Int32
	lastInvoke  atomic.Bool
}

func (g *statusGatewayStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	close(events)
}
func (g *statusGatewayStub) GetOwnerUserID() string { return "owner" }
func (g *statusGatewayStub) InjectSystemEvent(ctx context.Context, text string) error { return nil }
func (g *statusGatewayStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}
func (g *statusGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}
func (g *statusGatewayStub) HandoffCronResult(ctx context.Context, jobName, result string) error { return nil }
func (g *statusGatewayStub) InjectMessage(ctx context.Context, sessionKey, message string, invokeLLM bool, supervisor *user.User) error {
	g.injectCalls.Add(1)
	g.lastInvoke.Store(invokeLLM)
	return nil
}

func ownerStatusContext() context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel:    "http",
		ChatID:     "chat-1",
		SessionKey: "session-primary",
		User:       u,
	})
}

func TestSubagentStatusSteerInjectsGuidance(t *testing.T) {
	gw := &statusGatewayStub{}
	svc := cron.NewService(cron.NewStore("", ""), gw)
	svc.SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})
	runID, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		RequesterID:   "http:chat-1",
		SessionKey:    "subagent:steer",
		Prompt:        "long running",
		Purpose:       "test",
	})
	if err != nil {
		t.Fatalf("start run failed: %v", err)
	}

	tool := NewTool()
	_, err = tool.Execute(ownerStatusContext(), json.RawMessage(`{"action":"steer","runId":"`+runID+`","content":"adjust plan"}`))
	if err != nil {
		t.Fatalf("steer execute failed: %v", err)
	}
	if gw.injectCalls.Load() != 1 {
		t.Fatalf("expected one InjectMessage call, got %d", gw.injectCalls.Load())
	}
	if !gw.lastInvoke.Load() {
		t.Fatalf("expected steer to invoke llm")
	}
}

func TestSubagentStatusSendInjectsGhostwrite(t *testing.T) {
	gw := &statusGatewayStub{}
	svc := cron.NewService(cron.NewStore("", ""), gw)
	svc.SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})
	runID, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		RequesterID:   "http:chat-1",
		SessionKey:    "subagent:send",
		Prompt:        "long running",
		Purpose:       "test",
	})
	if err != nil {
		t.Fatalf("start run failed: %v", err)
	}

	tool := NewTool()
	_, err = tool.Execute(ownerStatusContext(), json.RawMessage(`{"action":"send","runId":"`+runID+`","content":"final note"}`))
	if err != nil {
		t.Fatalf("send execute failed: %v", err)
	}
	if gw.injectCalls.Load() != 1 {
		t.Fatalf("expected one InjectMessage call, got %d", gw.injectCalls.Load())
	}
	if gw.lastInvoke.Load() {
		t.Fatalf("expected send to avoid llm invocation")
	}
}
