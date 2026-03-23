package subagent_cancel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/cron"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type cancelToolGatewayStub struct{}

func (g *cancelToolGatewayStub) RunAgentForCron(ctx context.Context, req cron.AgentRequest, events chan<- cron.AgentEvent) {
	defer close(events)
	<-ctx.Done()
	events <- cron.AgentErrorEvent{Error: ctx.Err().Error()}
}
func (g *cancelToolGatewayStub) GetOwnerUserID() string { return "owner" }
func (g *cancelToolGatewayStub) InjectSystemEvent(ctx context.Context, text string) error { return nil }
func (g *cancelToolGatewayStub) DeliverAssistantOutput(ctx context.Context, userID string, msg delivery.AssistantMessage) delivery.Report {
	return delivery.Report{}
}
func (g *cancelToolGatewayStub) DeliverSystemMessage(ctx context.Context, userID string, msg delivery.SystemMessage) delivery.Report {
	return delivery.Report{}
}
func (g *cancelToolGatewayStub) HandoffCronResult(ctx context.Context, jobName, result string) error { return nil }

func ownerCtx() context.Context {
	u := &user.User{ID: "owner", Role: user.RoleOwner}
	return types.WithSessionContext(context.Background(), &types.SessionContext{
		Channel: "http",
		ChatID:  "chat-1",
		User:    u,
	})
}

func TestSubagentCancelCascadeCancelsParentAndChildren(t *testing.T) {
	svc := cron.NewService(cron.NewStore("", ""), &cancelToolGatewayStub{})
	svc.SetDelegatedRunsEnabled(true, "", delegatedrun.SpawnLimits{})

	parentID, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		RequesterType: "subagent",
		RequesterID:   "parent-req",
		SessionKey:    "subagent:parent",
		Prompt:        "parent",
		Purpose:       "test",
	})
	if err != nil {
		t.Fatalf("start parent failed: %v", err)
	}
	childID, err := svc.StartDelegatedRun(context.Background(), delegatedrun.RunSpec{
		ParentRunID:   parentID,
		RequesterType: "subagent",
		RequesterID:   "child-req",
		SessionKey:    "subagent:child",
		Prompt:        "child",
		Purpose:       "test",
	})
	if err != nil {
		t.Fatalf("start child failed: %v", err)
	}

	tool := NewTool()
	_, err = tool.Execute(ownerCtx(), json.RawMessage(`{"runId":"`+parentID+`","cascade":true}`))
	if err != nil {
		t.Fatalf("subagent_cancel execute failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		parentRec, parentOK := svc.GetDelegatedRun(parentID)
		childRec, childOK := svc.GetDelegatedRun(childID)
		if parentOK && childOK &&
			parentRec.State == delegatedrun.RunStateCanceled &&
			childRec.State == delegatedrun.RunStateCanceled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	parentRec, _ := svc.GetDelegatedRun(parentID)
	childRec, _ := svc.GetDelegatedRun(childID)
	t.Fatalf("expected both parent and child canceled; parent=%s child=%s", parentRec.State, childRec.State)
}

