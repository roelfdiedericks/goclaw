package http

import (
	"context"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/acp"
	"github.com/roelfdiedericks/goclaw/internal/delegatedrun"
	"github.com/roelfdiedericks/goclaw/internal/gateway"
	gwtypes "github.com/roelfdiedericks/goclaw/internal/gateway/types"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

type handoffGatewayStub struct {
	handoffSessionKey string
	runCalled         bool
}

func (s *handoffGatewayStub) RunAgent(ctx context.Context, req gateway.AgentRequest, events chan<- gateway.AgentEvent) error {
	s.runCalled = true
	close(events)
	return nil
}

func (s *handoffGatewayStub) AgentIdentity() *gwtypes.AgentIdentityConfig   { return nil }
func (s *handoffGatewayStub) SupervisionConfig() *gwtypes.SupervisionConfig { return nil }
func (s *handoffGatewayStub) ACPRespond(sessionKey string, resp acp.ACPDriverExtensionResponse) error {
	return nil
}
func (s *handoffGatewayStub) ACPHandoffPending(ctx context.Context, sessionKey string) ([]acp.AttachmentPendingRequestInfo, error) {
	s.handoffSessionKey = sessionKey
	return []acp.AttachmentPendingRequestInfo{{
		Driver:     "cursor",
		Method:     "cursor/ask_question",
		ToolCallID: "tool-123",
	}}, nil
}
func (s *handoffGatewayStub) StopAllUserSessions(userID string) (int, error) { return 0, nil }
func (s *handoffGatewayStub) RequestShutdown(userID string) error             { return nil }
func (s *handoffGatewayStub) ListDelegatedRuns() []delegatedrun.RunRecord     { return nil }
func (s *handoffGatewayStub) GetDelegatedRun(runID string) (delegatedrun.RunRecord, bool) {
	return delegatedrun.RunRecord{}, false
}
func (s *handoffGatewayStub) CancelDelegatedRun(runID string) error { return nil }
func (s *handoffGatewayStub) ListDelegatedRunEvents(sinceID int64, limit int) []delegatedrun.RunEvent {
	return nil
}

func TestRunAgentRequestCancelsPendingACPInteractionBeforeNewTurn(t *testing.T) {
	channel := NewHTTPChannel(nil)
	gatewayStub := &handoffGatewayStub{}
	channel.SetGateway(gatewayStub)

	u := &user.User{ID: "owner", Role: user.RoleOwner}
	sessionID := "http-session-1"
	channel.sessions[sessionID] = &SSESession{
		SessionID: sessionID,
		UserID:    u.ID,
		User:      u,
	}

	if err := channel.RunAgentRequest(context.Background(), sessionID, u, "continue in chat", nil); err != nil {
		t.Fatalf("run agent request failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gatewayStub.runCalled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gatewayStub.runCalled {
		t.Fatalf("expected RunAgent to be called")
	}
	if gatewayStub.handoffSessionKey != "primary" {
		t.Fatalf("expected handoff session key primary, got %q", gatewayStub.handoffSessionKey)
	}

	sess := channel.sessions[sessionID]
	sess.bufferMu.Lock()
	defer sess.bufferMu.Unlock()
	if len(sess.eventBuffer) == 0 {
		t.Fatalf("expected cancellation event to be buffered")
	}
	last := sess.eventBuffer[len(sess.eventBuffer)-1].Event
	if last.Event != "acp_interaction_cancelled" {
		t.Fatalf("expected acp_interaction_cancelled event, got %q", last.Event)
	}
	data, ok := last.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected cancellation data map, got %T", last.Data)
	}
	if data["toolCallId"] != "tool-123" {
		t.Fatalf("expected toolCallId tool-123, got %#v", data["toolCallId"])
	}
}
